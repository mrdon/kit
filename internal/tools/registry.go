package tools

import (
	"context"
	"encoding/json"
	"fmt"

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
	return &services.Caller{
		TenantID: ec.Tenant.ID,
		UserID:   ec.User.ID,
		Identity: ec.User.SlackUserID,
		Roles:    roles,
		IsAdmin:  ec.User.IsAdmin,
		Timezone: services.ResolveTimezone(ec.User.Timezone, ec.Tenant.Timezone),
	}
}

// HandlerFunc executes a tool and returns a string result.
type HandlerFunc func(ec *ExecContext, input json.RawMessage) (string, error)

// Def defines a single tool.
type Def struct {
	Name        string
	Description string
	Schema      map[string]any
	Handler     HandlerFunc
	AdminOnly   bool
	Terminal    bool // if true, calling this tool ends the agent loop
}

// Registry holds all registered tools.
type Registry struct {
	defs     []Def
	handlers map[string]HandlerFunc
}

// NewRegistry creates a registry and runs all register functions for the
// given user. botInitiated toggles which messaging tools are registered:
// live Slack conversations get reply_in_thread; bot-initiated runs
// (scheduled tasks, decision resolves) do not — the agent must pick a
// named channel or user explicitly.
func NewRegistry(isAdmin, botInitiated bool) *Registry {
	r := &Registry{handlers: make(map[string]HandlerFunc)}

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

	return r
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
