package mcp

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/agent"
	"github.com/mrdon/kit/internal/anthropic"
	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/scheduler"
	"github.com/mrdon/kit/internal/services"
)

// ServerHolder wraps the MCPServer so external code can reach the registered server.
type ServerHolder struct {
	Server *mcpserver.MCPServer
	pool   *pgxpool.Pool
	svc    *services.Services
	agent  *agent.Agent
	enc    *crypto.Encryptor
	sched  *scheduler.Scheduler
}

// NewServer creates an MCP server with all tools registered at the server level.
// Each handler resolves the authenticated caller from ctx at call time via the
// mcpauth.WithCaller wrapper, so restarts don't wipe client-visible tool state.
// Admin-only tools are hidden from non-admins via a ToolFilter on tools/list;
// role-gated tools (ToolMeta.VisibleToRoles) are hidden from callers who hold
// none of the listed roles. The service layer still enforces authorization
// independently — the filter is a discovery-only surface.
func NewServer(pool *pgxpool.Pool, svc *services.Services, a *agent.Agent, enc *crypto.Encryptor, sched *scheduler.Scheduler) *ServerHolder {
	sh := &ServerHolder{pool: pool, svc: svc, agent: a, enc: enc, sched: sched}

	adminOnly, roleGated := collectToolVisibility()

	// Builder-published tools ride on per-session tool maps
	// (SessionWithTools) — the register/unregister hooks seed + tear down
	// those maps, and publish/revoke fan-outs push list_changed
	// notifications on the fly. See exposed_tools.go.
	hooks := &mcpserver.Hooks{}
	hooks.AddOnRegisterSession(onRegisterSession)
	hooks.AddOnUnregisterSession(onUnregisterSession)

	sh.Server = mcpserver.NewMCPServer(
		"kit",
		"1.0.0",
		mcpserver.WithToolCapabilities(true),
		mcpserver.WithResourceCapabilities(true, false),
		mcpserver.WithHooks(hooks),
		mcpserver.WithToolFilter(func(ctx context.Context, tools []mcp.Tool) []mcp.Tool {
			caller := auth.CallerFromContext(ctx)
			filtered := make([]mcp.Tool, 0, len(tools))
			for _, t := range tools {
				if adminOnly[t.Name] && (caller == nil || !caller.IsAdmin) {
					continue
				}
				if roles, ok := roleGated[t.Name]; ok {
					if caller == nil || !anyRoleIntersect(caller.Roles, roles) {
						continue
					}
				}
				filtered = append(filtered, t)
			}
			return filtered
		}),
	)

	registerResources(sh.Server, pool, svc)

	tools := buildAllTools(pool, svc, a, enc, sched, a.LLM())
	sh.Server.AddTools(tools...)

	toolNames := make([]string, len(tools))
	for i, t := range tools {
		toolNames[i] = t.Tool.Name
	}
	slog.Info("mcp server tools registered", "tool_count", len(tools), "tools", toolNames)

	return sh
}

// buildAllTools collects every MCP tool — core + app-contributed — into a single
// slice for server-level registration. Handlers resolve the caller per request.
// llm is threaded in for handlers that do one-shot Claude calls (e.g. the
// create_task classifier); groups that don't need it ignore the param.
func buildAllTools(pool *pgxpool.Pool, svc *services.Services, a *agent.Agent, enc *crypto.Encryptor, sched *scheduler.Scheduler, llm *anthropic.Client) []mcpserver.ServerTool {
	type handlerFn func(string, *pgxpool.Pool, *services.Services) mcpserver.ToolHandlerFunc
	// Uniform shim so every handler can be invoked with the same arg set,
	// regardless of whether it uses llm. Special-cased groups (TaskTools)
	// register their handler directly below.
	wrap := func(h handlerFn) func(string, *pgxpool.Pool, *services.Services, *anthropic.Client) mcpserver.ToolHandlerFunc {
		return func(name string, pool *pgxpool.Pool, svc *services.Services, _ *anthropic.Client) mcpserver.ToolHandlerFunc {
			return h(name, pool, svc)
		}
	}
	allMetas := []struct {
		metas   []services.ToolMeta
		handler func(string, *pgxpool.Pool, *services.Services, *anthropic.Client) mcpserver.ToolHandlerFunc
	}{
		{services.SkillTools, wrap(skillMCPHandler)},
		{services.RuleTools, wrap(ruleMCPHandler)},
		{services.MemoryTools, wrap(memoryMCPHandler)},
		{services.RoleTools, wrap(roleMCPHandler)},
		{services.TaskTools, taskMCPHandler},
		{services.TenantTools, wrap(tenantMCPHandler)},
		{services.UserTools, wrap(userMCPHandler)},
		{services.SessionTools, wrap(sessionMCPHandler)},
	}

	var out []mcpserver.ServerTool
	for _, group := range allMetas {
		for _, meta := range group.metas {
			schema := services.InjectRequireApprovalSchema(meta.Schema)
			schemaJSON, _ := json.Marshal(schema)
			tool := mcp.NewToolWithRawSchema(meta.Name, meta.Description, schemaJSON)
			inner := group.handler(meta.Name, pool, svc, llm)
			out = append(out, mcpserver.ServerTool{
				Tool:    tool,
				Handler: gatedMCP(meta.Name, inner),
			})
		}
	}
	// App-contributed tools already have their own schemas; inject + wrap
	// them too so require_approval works consistently across surfaces.
	for _, t := range apps.BuildMCPTools(pool, svc) {
		out = append(out, wrapAppTool(t))
	}

	// run_task needs agent + enc + scheduler, registered separately from the standard loop
	out = append(out, wrapAppTool(buildRunTaskTool(pool, svc, a, enc, sched)))

	return out
}

// wrapAppTool re-marshals the tool's already-registered schema with the
// require_approval field injected, and wraps its handler with the gate
// middleware. App-supplied tools arrive with a raw-JSON schema (set on
// RawInputSchema by mcp.NewToolWithRawSchema) rather than the typed
// InputSchema struct, so we read RawInputSchema first and fall back to
// marshaling InputSchema for tools built with mcp.NewTool.
func wrapAppTool(t mcpserver.ServerTool) mcpserver.ServerTool {
	var schemaBytes []byte
	if len(t.Tool.RawInputSchema) > 0 {
		schemaBytes = t.Tool.RawInputSchema
	} else {
		// NewTool path: marshal the typed schema to raw JSON so we can
		// inject and re-attach.
		b, err := json.Marshal(t.Tool.InputSchema)
		if err == nil {
			schemaBytes = b
		}
	}
	if len(schemaBytes) > 0 {
		var schema map[string]any
		if err := json.Unmarshal(schemaBytes, &schema); err == nil {
			schema = services.InjectRequireApprovalSchema(schema)
			if updated, err := json.Marshal(schema); err == nil {
				t.Tool = mcp.NewToolWithRawSchema(t.Tool.Name, t.Tool.Description, updated)
			}
		}
	}
	t.Handler = gatedMCP(t.Tool.Name, t.Handler)
	return t
}

// collectToolVisibility walks every core service ToolMeta group plus each
// registered app's ToolMetas, producing two maps for the tools/list filter:
//
//   - adminOnly: tool names that should be hidden from non-admin callers
//   - roleGated: tool name → visible_to_roles; tool is hidden from callers
//     that hold none of the listed roles. Empty lists are not recorded
//     (empty means "visible to all subject to AdminOnly").
func collectToolVisibility() (adminOnly map[string]bool, roleGated map[string][]string) {
	adminOnly = map[string]bool{}
	roleGated = map[string][]string{}
	groups := [][]services.ToolMeta{
		services.SkillTools, services.RuleTools, services.MemoryTools,
		services.RoleTools, services.TaskTools, services.TenantTools,
		services.UserTools, services.SessionTools,
	}
	record := func(m services.ToolMeta) {
		if m.AdminOnly {
			adminOnly[m.Name] = true
		}
		if len(m.VisibleToRoles) > 0 {
			roleGated[m.Name] = m.VisibleToRoles
		}
	}
	for _, g := range groups {
		for _, m := range g {
			record(m)
		}
	}
	for _, a := range apps.All() {
		for _, m := range a.ToolMetas() {
			record(m)
		}
	}
	return adminOnly, roleGated
}

// anyRoleIntersect reports whether caller holds at least one of the listed
// roles. Duplicates the registry's helper to keep mcp free of a
// tools-package import (the current MCP package deliberately stays narrow).
func anyRoleIntersect(have, want []string) bool {
	if len(have) == 0 || len(want) == 0 {
		return false
	}
	set := make(map[string]struct{}, len(have))
	for _, r := range have {
		set[r] = struct{}{}
	}
	for _, r := range want {
		if _, ok := set[r]; ok {
			return true
		}
	}
	return false
}
