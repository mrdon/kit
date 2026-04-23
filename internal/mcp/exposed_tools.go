// Package mcp: exposed_tools.go serves builder-published tools via
// per-session tool maps (mcp-go's SessionWithTools). On every new MCP
// session we load the caller's published tools from exposed_tools and
// install them with SetSessionTools. When admins publish/revoke mid-
// session, we fan the change out to every live session in the tenant
// via AddSessionTools / DeleteSessionTools — both fire
// notifications/tools/list_changed automatically.
//
// No tenant-prefixing is needed: each session's tool map is scoped to
// the caller who established it, so cross-tenant isolation is free.
// Static global tools (skill_*, rule_*, etc.) continue to live on the
// MCPServer; session tools shadow globals on name collision.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/mcpauth"
	"github.com/mrdon/kit/internal/services"
)

// ExposedToolInvoker dispatches an invocation of a published tool. Wired
// from cmd/kit/main.go as a closure over builder.InvokeExposedTool +
// pool, so this package doesn't import apps/builder directly.
type ExposedToolInvoker func(ctx context.Context, caller *services.Caller, toolName string, args map[string]any) (string, error)

// sessionToolMutator is the slice of mcp-go's MCPServer API that
// PublishExposedTool / RevokeExposedTool need. Declared as an interface
// so unit tests can stub the server without constructing a real MCPServer
// + registering real sessions.
type sessionToolMutator interface {
	AddSessionTools(sessionID string, tools ...mcpserver.ServerTool) error
	DeleteSessionTools(sessionID string, names ...string) error
}

// exposedRegistry is the package-wide state the per-session hooks and
// publish/revoke helpers close over. Installed once at startup by
// InstallExposedToolRegistry (called from cmd/kit/main.go after
// NewServer returns). Kept as a pointer so tests can swap it wholesale.
type exposedRegistry struct {
	server sessionToolMutator
	pool   *pgxpool.Pool
	invoke ExposedToolInvoker

	mu sync.RWMutex
	// sessionID → tenantID, so unregister can drop from the tenant set.
	sessionTenant map[string]uuid.UUID
	// tenantID → set of sessionIDs, for publish/revoke fan-out.
	tenantSessions map[uuid.UUID]map[string]struct{}
}

var currentExposedRegistry *exposedRegistry

// InstallExposedToolRegistry wires the MCP server + pool + invoke hook
// used by the session register/unregister hooks and the publish/revoke
// helpers. Safe to call multiple times (latest wins); tests use this to
// reset state between cases.
func InstallExposedToolRegistry(holder *ServerHolder, invoker ExposedToolInvoker) {
	if holder == nil || holder.Server == nil {
		currentExposedRegistry = nil
		return
	}
	currentExposedRegistry = &exposedRegistry{
		server:         holder.Server,
		pool:           holder.pool,
		invoke:         invoker,
		sessionTenant:  make(map[string]uuid.UUID),
		tenantSessions: make(map[uuid.UUID]map[string]struct{}),
	}
}

// resetExposedRegistryForTest is a test-only helper so unit tests in
// other files can swap the registry without leaking package-internal
// state. Safe for production (no callers).
func resetExposedRegistryForTest(reg *exposedRegistry) { currentExposedRegistry = reg }

// onRegisterSession seeds the session's tool map with the caller's
// tenant-scoped published tools. No-ops cleanly when:
//   - caller isn't authenticated (initialization is gated by our auth
//     middleware, but stay defensive);
//   - transport doesn't implement SessionWithTools;
//   - no registry wired (tests, early startup).
func onRegisterSession(ctx context.Context, session mcpserver.ClientSession) {
	reg := currentExposedRegistry
	if reg == nil {
		return
	}
	caller := auth.CallerFromContext(ctx)
	if caller == nil {
		return
	}
	st, ok := session.(mcpserver.SessionWithTools)
	if !ok {
		return
	}

	tools, err := reg.loadTenantTools(ctx, caller)
	if err != nil {
		slog.Warn("loading exposed tools for session", "tenant_id", caller.TenantID, "error", err)
		return
	}

	toolMap := make(map[string]mcpserver.ServerTool, len(tools))
	for _, t := range tools {
		toolMap[t.Tool.Name] = t
	}
	st.SetSessionTools(toolMap)

	sid := session.SessionID()
	reg.mu.Lock()
	reg.sessionTenant[sid] = caller.TenantID
	set, ok := reg.tenantSessions[caller.TenantID]
	if !ok {
		set = make(map[string]struct{})
		reg.tenantSessions[caller.TenantID] = set
	}
	set[sid] = struct{}{}
	reg.mu.Unlock()

	slog.Info("mcp session exposed tools loaded",
		"session_id", sid,
		"tenant_id", caller.TenantID,
		"tool_count", len(toolMap),
	)
}

// onUnregisterSession drops the session from our fan-out tracking so the
// tenant's set stays accurate.
func onUnregisterSession(_ context.Context, session mcpserver.ClientSession) {
	reg := currentExposedRegistry
	if reg == nil {
		return
	}
	sid := session.SessionID()
	reg.mu.Lock()
	defer reg.mu.Unlock()
	tid, ok := reg.sessionTenant[sid]
	if !ok {
		return
	}
	delete(reg.sessionTenant, sid)
	if set := reg.tenantSessions[tid]; set != nil {
		delete(set, sid)
		if len(set) == 0 {
			delete(reg.tenantSessions, tid)
		}
	}
}

// PublishExposedTool is installed as builder.SetExposedToolHooks' publish
// hook. Loads the freshly-inserted row and fans a new session tool out
// to every live session in the tenant (which includes the admin's own
// session — AddSessionTools fires list_changed on each).
func PublishExposedTool(ctx context.Context, caller *services.Caller, toolName string) error {
	reg := currentExposedRegistry
	if reg == nil || reg.server == nil || reg.pool == nil {
		return nil
	}
	if caller == nil {
		return errors.New("publish exposed tool: caller required")
	}
	tool, err := reg.loadToolByName(ctx, caller, toolName)
	if err != nil {
		return err
	}
	for _, sid := range reg.sessionIDsForTenant(caller.TenantID) {
		if err := reg.server.AddSessionTools(sid, tool); err != nil {
			slog.Warn("mcp add session tool",
				"session_id", sid, "tool", toolName, "error", err)
		}
	}
	return nil
}

// RevokeExposedTool is the revoke hook. Removes the named tool from
// every live session in the tenant; each session gets a list_changed.
func RevokeExposedTool(ctx context.Context, caller *services.Caller, toolName string) error {
	reg := currentExposedRegistry
	if reg == nil || reg.server == nil {
		return nil
	}
	if caller == nil {
		return errors.New("revoke exposed tool: caller required")
	}
	for _, sid := range reg.sessionIDsForTenant(caller.TenantID) {
		if err := reg.server.DeleteSessionTools(sid, toolName); err != nil {
			slog.Warn("mcp delete session tool",
				"session_id", sid, "tool", toolName, "error", err)
		}
	}
	return nil
}

func (r *exposedRegistry) sessionIDsForTenant(tenantID uuid.UUID) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	set := r.tenantSessions[tenantID]
	out := make([]string, 0, len(set))
	for sid := range set {
		out = append(out, sid)
	}
	return out
}

// loadTenantTools reads every non-stale exposed_tools row for the
// caller's tenant, filters by role visibility, and constructs
// ServerTool entries ready for SetSessionTools.
func (r *exposedRegistry) loadTenantTools(ctx context.Context, caller *services.Caller) ([]mcpserver.ServerTool, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT et.tool_name, COALESCE(et.description, ''),
		       et.args_schema, COALESCE(et.visible_to_roles, '{}'::text[])
		FROM exposed_tools et
		JOIN scripts s       ON s.id  = et.script_id
		JOIN builder_apps ba ON ba.id = s.builder_app_id
		WHERE et.tenant_id = $1 AND et.is_stale = false
	`, caller.TenantID)
	if err != nil {
		return nil, fmt.Errorf("querying exposed_tools: %w", err)
	}
	defer rows.Close()

	out := make([]mcpserver.ServerTool, 0)
	for rows.Next() {
		var (
			toolName, desc string
			argsSchema     []byte
			roles          []string
		)
		if err := rows.Scan(&toolName, &desc, &argsSchema, &roles); err != nil {
			return nil, fmt.Errorf("scan exposed_tools row: %w", err)
		}
		if !isExposedToolVisible(caller, roles) {
			continue
		}
		out = append(out, r.buildServerTool(toolName, desc, argsSchema))
	}
	return out, rows.Err()
}

// loadToolByName reads a single published tool by name within the
// caller's tenant. Used by PublishExposedTool to build the ServerTool
// after insert.
func (r *exposedRegistry) loadToolByName(ctx context.Context, caller *services.Caller, toolName string) (mcpserver.ServerTool, error) {
	var (
		desc       string
		argsSchema []byte
		roles      []string
	)
	err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(et.description, ''),
		       et.args_schema, COALESCE(et.visible_to_roles, '{}'::text[])
		FROM exposed_tools et
		WHERE et.tenant_id = $1 AND et.tool_name = $2
	`, caller.TenantID, toolName).Scan(&desc, &argsSchema, &roles)
	if err != nil {
		return mcpserver.ServerTool{}, fmt.Errorf("loading exposed_tool %q: %w", toolName, err)
	}
	_ = roles // publish fan-out installs on sessions regardless of the
	// admin's own role — recipients' per-session loads already ran role
	// filtering at register time. An edge case (mid-session role change)
	// is acceptable: session tools rebalance on reconnect.
	return r.buildServerTool(toolName, desc, argsSchema), nil
}

// buildServerTool assembles one ServerTool: schema with require_approval
// injected, handler that calls the invoke hook, wrapped with gatedMCP so
// `require_approval: true` lands on the decision-card path.
func (r *exposedRegistry) buildServerTool(toolName, description string, argsSchema []byte) mcpserver.ServerTool {
	schema := map[string]any{}
	if len(argsSchema) > 0 {
		_ = json.Unmarshal(argsSchema, &schema)
	}
	if _, ok := schema["type"]; !ok {
		schema = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	schema = services.InjectRequireApprovalSchema(schema)
	schemaJSON, _ := json.Marshal(schema)

	invoke := r.invoke
	name := toolName // capture
	handler := mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		if invoke == nil {
			return mcp.NewToolResultError("exposed tool invoker not wired"), nil
		}
		// The universal require_approval flag is stripped by gatedMCP
		// before we get here; any remaining args are the script's.
		args := req.GetArguments()
		out, err := invoke(ctx, caller, name, args)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(out), nil
	})

	return mcpserver.ServerTool{
		Tool:    mcp.NewToolWithRawSchema(toolName, description, schemaJSON),
		Handler: gatedMCP(toolName, handler),
	}
}

// isExposedToolVisible mirrors tools.IsDefVisible's rule without pulling
// in the tools package (mcp stays narrow). Admin sees everything in
// their tenant; empty visible_to_roles means visible to all; a non-
// empty list requires a role intersection.
func isExposedToolVisible(caller *services.Caller, visibleToRoles []string) bool {
	if caller == nil {
		return false
	}
	if caller.IsAdmin {
		return true
	}
	if len(visibleToRoles) == 0 {
		return true
	}
	return anyRoleIntersect(caller.Roles, visibleToRoles)
}
