package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/anthropic"
	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	kitslack "github.com/mrdon/kit/internal/slack"
	"github.com/mrdon/kit/internal/web"
)

// ExecContext holds everything a tool needs to execute.
//
// Responder, OnToolCall, and OnIteration are observability/redirection
// seams used by the chat SSE path. Slack and scheduled-task callers
// leave them unset; agent.go treats nil values as no-op so there is one
// code path through the loop regardless of caller.
type ExecContext struct {
	Ctx      context.Context
	Pool     *pgxpool.Pool
	Slack    *kitslack.Client
	Fetcher  *web.Fetcher
	Tenant   *models.Tenant
	User     *models.User
	Session  *models.Session
	Channel  string
	ThreadTS string
	Svc      *services.Services

	// TaskID is set when this run is executing a scheduled task. Tools
	// that want to link artifacts back to the originating task (e.g.
	// create_decision stamping origin_task_id) read this. Nil for
	// user-initiated Slack or chat runs.
	TaskID *uuid.UUID

	// Responder is where reply_in_thread sends its output. When nil, the
	// handler constructs a SlackResponder on demand (default behavior).
	Responder Responder

	// OnToolCall fires just before a tool's handler runs. Used by the
	// chat path to emit a "tool" SSE event.
	OnToolCall func(name string)

	// OnIteration fires at the top of each agent-loop iteration, before
	// the LLM call. Used by the chat path to flip the UI status line
	// back to "thinking" between tool calls.
	OnIteration func()
}

// Caller builds a services.Caller from the current execution context.
func (ec *ExecContext) Caller() *services.Caller {
	roles, _ := models.GetUserRoleNames(ec.Ctx, ec.Pool, ec.Tenant.ID, ec.User.ID, ec.Tenant.DefaultRoleID)
	roleIDs, _ := models.GetUserRoleIDs(ec.Ctx, ec.Pool, ec.Tenant.ID, ec.User.ID, ec.Tenant.DefaultRoleID)
	return &services.Caller{
		TenantID: ec.Tenant.ID,
		UserID:   ec.User.ID,
		Identity: ec.User.SlackUserID,
		Roles:    roles,
		RoleIDs:  roleIDs,
		IsAdmin:  ec.User.IsAdmin,
		Timezone: services.ResolveTimezone(ec.User.Timezone, ec.Tenant.Timezone),
	}
}

// HandlerFunc executes a tool and returns a string result.
type HandlerFunc func(ec *ExecContext, input json.RawMessage) (string, error)

// Def defines a single tool.
//
// VisibleToRoles gates catalog visibility independent of AdminOnly:
//   - empty slice → visible to every caller (subject to AdminOnly)
//   - non-empty  → only callers who hold at least one listed role see it
//
// Builder-exposed tools use VisibleToRoles to scope tenant-specific scripts
// to the roles their author chose (e.g. a "lookup" tool visible only to
// "bartender"). Static Kit tools leave it empty.
type Def struct {
	Name           string
	Description    string
	Schema         map[string]any
	Handler        HandlerFunc
	AdminOnly      bool
	VisibleToRoles []string
	Terminal       bool // if true, calling this tool ends the agent loop
}

// Registry holds all registered tools.
type Registry struct {
	defs     []Def
	handlers map[string]HandlerFunc
}

// ExposedToolDef describes one tenant-published script function surfaced
// through the generic tool registry. Registry construction asks the
// registered ExposedToolRunner to enumerate these per caller, then wraps
// each with a Def whose handler invokes the backing script.
type ExposedToolDef struct {
	ToolName       string
	Description    string
	ArgsSchema     map[string]any
	VisibleToRoles []string
	// Invoke is a closure that runs the exposed tool with the supplied
	// keyword args. Implementations are responsible for enforcing stale
	// flags, role checks at invocation time, and child audit rows. The
	// registry treats the returned string as the tool result.
	Invoke func(ctx context.Context, ec *ExecContext, args map[string]any) (string, error)
}

// ExposedToolRunner is implemented by the builder app (or any future
// source of dynamic tools). The registry calls List at construction time
// to enumerate the caller's tenant-published tools. A nil runner makes
// dynamic registration a no-op — static Kit tools still register
// normally, so a mis-wired startup doesn't break the agent.
//
// Split out into its own interface to dodge the import cycle: the
// builder package imports tools (to register Defs); if tools imported
// builder to call RunScript directly we'd have a cycle. The interface
// hop means the builder registers an implementation during app Init and
// the tools package only depends on the narrow contract declared here.
type ExposedToolRunner interface {
	// List returns the exposed tools for the given caller's tenant.
	// Implementations should filter out stale rows so the registry can
	// trust what it receives (registry applies the role-visibility check
	// centrally via Def.VisibleToRoles).
	List(ctx context.Context, caller *services.Caller) ([]ExposedToolDef, error)
}

// currentExposedToolRunner is wired once at startup from cmd/kit/main.go
// (after apps Init). A nil value is allowed — dynamic registration simply
// short-circuits and the static tool set is returned.
var currentExposedToolRunner ExposedToolRunner

// SetExposedToolRunner wires the tenant-exposed tool source used by
// NewRegistry. Call once during startup, after apps have initialized.
// Passing nil disables dynamic registration (tests reset via t.Cleanup).
func SetExposedToolRunner(r ExposedToolRunner) {
	currentExposedToolRunner = r
}

// NewRegistry creates a registry and runs all register functions for the
// given caller. botInitiated toggles which messaging tools are registered:
// live Slack conversations get reply_in_thread; bot-initiated runs
// (scheduled tasks, decision resolves) do not — the agent must pick a
// named channel or user explicitly.
//
// The caller determines two things: admin gating for admin-only static
// tools (preserved from the pre-4d signature via caller.IsAdmin) and the
// pool+tenant used to fetch tenant-specific dynamic tools. Passing a nil
// caller is allowed in tests that don't exercise dynamic tools; admin
// tools are simply skipped in that case.
func NewRegistry(ctx context.Context, caller *services.Caller, botInitiated bool) *Registry {
	r := &Registry{handlers: make(map[string]HandlerFunc)}

	isAdmin := caller != nil && caller.IsAdmin

	// Each tool group registers itself here.
	// To add a new tool: create a file, add a Register call below.
	registerCoreTools(r, botInitiated)
	registerSkillTools(r, isAdmin)
	registerRoleTools(r, isAdmin)
	registerRuleTools(r, isAdmin)
	registerMemoryTools(r, isAdmin)
	registerTenantTools(r, isAdmin)
	registerWebTools(r)
	registerTaskTools(r, isAdmin)
	registerUserTools(r)

	// App tools
	for _, a := range apps.All() {
		a.RegisterAgentTools(r, isAdmin)
	}

	// Dynamic per-tenant exposed tools. Skipped silently if no runner is
	// wired (tests, or a startup ordering slip). Failures to enumerate are
	// logged but non-fatal — the agent still works with the static set.
	if currentExposedToolRunner != nil && caller != nil {
		defs, err := currentExposedToolRunner.List(ctx, caller)
		if err != nil {
			slog.Warn("listing exposed tools", "tenant_id", caller.TenantID, "error", err)
		} else {
			for _, d := range defs {
				r.Register(buildExposedDef(d))
			}
		}
	}

	return r
}

// buildExposedDef wraps an ExposedToolDef into a tools.Def whose handler
// unmarshals the raw input, forwards to the runner-supplied Invoke, and
// returns the string result. The closure captures d by value so repeated
// registry builds don't share a mutable reference.
func buildExposedDef(d ExposedToolDef) Def {
	captured := d
	return Def{
		Name:           captured.ToolName,
		Description:    captured.Description,
		Schema:         captured.ArgsSchema,
		VisibleToRoles: captured.VisibleToRoles,
		Handler: func(ec *ExecContext, input json.RawMessage) (string, error) {
			args := map[string]any{}
			if len(input) > 0 {
				if err := json.Unmarshal(input, &args); err != nil {
					return "", fmt.Errorf("exposed tool %s: invalid arguments: %w", captured.ToolName, err)
				}
			}
			return captured.Invoke(ec.Ctx, ec, args)
		},
	}
}

// Register adds a tool to the registry.
func (r *Registry) Register(d Def) {
	r.defs = append(r.defs, d)
	r.handlers[d.Name] = d.Handler
}

// alwaysLoaded is the set of tool names that are sent in the request
// prefix on every iteration. Everything else is marked DeferLoading=true
// so the model must discover it via tool_search_tool.
//
// The set is intentionally tight (~5 tools): the three terminal
// messaging tools so the model can always end a turn; search_skills as
// the existing capability-discovery surface; find_user because almost
// every Slack interaction needs to resolve a mention. Add a name here
// only if the model needs it from the very first turn without a
// search round-trip.
var alwaysLoaded = map[string]bool{
	"reply_in_thread": true,
	"post_to_channel": true,
	"dm_user":         true,
	"search_skills":   true,
	"find_user":       true,
}

// Definitions returns tool definitions for the Claude API. Tools not in
// alwaysLoaded are marked DeferLoading=true; their schemas stay out of
// the prefix and only enter context when the model invokes
// tool_search_tool to find them.
func (r *Registry) Definitions() []anthropic.Tool {
	var tools []anthropic.Tool
	for _, d := range r.defs {
		tools = append(tools, anthropic.Tool{
			Name:         d.Name,
			Description:  d.Description,
			InputSchema:  d.Schema,
			DeferLoading: !alwaysLoaded[d.Name],
		})
	}
	return tools
}

// DefinitionsFor returns Definitions() filtered for the given caller:
// AdminOnly tools are omitted unless caller.IsAdmin, and VisibleToRoles
// tools are omitted unless caller holds at least one listed role.
// Handlers set via Execute remain callable by name — the filter governs
// catalog/discovery surfaces only. A nil caller is treated as a
// non-admin with no roles (maximally restrictive).
func (r *Registry) DefinitionsFor(caller *services.Caller) []anthropic.Tool {
	var out []anthropic.Tool
	for _, d := range r.defs {
		if !IsDefVisible(d, caller) {
			continue
		}
		out = append(out, anthropic.Tool{
			Name:         d.Name,
			Description:  d.Description,
			InputSchema:  d.Schema,
			DeferLoading: !alwaysLoaded[d.Name],
		})
	}
	return out
}

// IsDefVisible centralises the visibility rule so registry, MCP, and
// tests share one predicate. See DefinitionsFor for semantics.
func IsDefVisible(d Def, caller *services.Caller) bool {
	if d.AdminOnly {
		if caller == nil || !caller.IsAdmin {
			return false
		}
	}
	if len(d.VisibleToRoles) > 0 {
		if caller == nil {
			return false
		}
		if !anyIntersect(caller.Roles, d.VisibleToRoles) {
			return false
		}
	}
	return true
}

// anyIntersect reports whether slices a and b share at least one element.
// Case-sensitive to match role-name comparisons elsewhere in Kit.
func anyIntersect(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	set := make(map[string]struct{}, len(a))
	for _, x := range a {
		set[x] = struct{}{}
	}
	for _, y := range b {
		if _, ok := set[y]; ok {
			return true
		}
	}
	return false
}

// Execute runs a tool by name.
func (r *Registry) Execute(ec *ExecContext, name string, input json.RawMessage) (string, error) {
	fn, ok := r.handlers[name]
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	return fn(ec, input)
}

// IsTerminal returns true if calling this tool should end the agent loop.
// All three messaging tools are terminal — any single post is the agent's
// output for that run. A task that needs to fan out to multiple targets
// can emit parallel tool calls in one turn, or be split into separate
// decision options.
func (r *Registry) IsTerminal(name string, _ json.RawMessage, _ string) bool {
	for _, d := range r.defs {
		if d.Name == name {
			return d.Terminal
		}
	}
	return false
}

// propsReq builds a JSON schema with required fields.
func propsReq(fields map[string]any, required ...string) map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": fields,
		"required":   required,
	}
}

// field is a shorthand for a schema field.
func field(typ, desc string) map[string]any {
	return map[string]any{"type": typ, "description": desc}
}
