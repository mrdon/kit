package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/anthropic"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	kitslack "github.com/mrdon/kit/internal/slack"
	"github.com/mrdon/kit/internal/web"
)

// ExecContext holds everything a tool needs to execute.
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

// NewRegistry creates a registry and runs all register functions for the given user.
func NewRegistry(isAdmin bool) *Registry {
	r := &Registry{handlers: make(map[string]HandlerFunc)}

	// Each tool group registers itself here.
	// To add a new tool: create a file, add a Register call below.
	registerCoreTools(r)
	registerSkillTools(r, isAdmin)
	registerRoleTools(r, isAdmin)
	registerRuleTools(r, isAdmin)
	registerMemoryTools(r, isAdmin)
	registerTenantTools(r, isAdmin)
	registerWebTools(r)
	registerTaskTools(r, isAdmin)

	return r
}

// Register adds a tool to the registry.
func (r *Registry) Register(d Def) {
	r.defs = append(r.defs, d)
	r.handlers[d.Name] = d.Handler
}

// Definitions returns tool definitions for the Claude API.
func (r *Registry) Definitions() []anthropic.Tool {
	var tools []anthropic.Tool
	for _, d := range r.defs {
		tools = append(tools, anthropic.Tool{
			Name:        d.Name,
			Description: d.Description,
			InputSchema: d.Schema,
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
func (r *Registry) IsTerminal(name string) bool {
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
