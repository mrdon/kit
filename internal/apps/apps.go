package apps

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/services"
)

// AgentToolRegisterer is satisfied by *tools.Registry.
// Defined here to avoid an import cycle between apps and tools.
type AgentToolRegisterer interface {
	RegisterDef(name, description string, schema map[string]any, adminOnly bool, handler any)
}

// App defines the interface for a modular feature that contributes tools,
// routes, cron jobs, and system prompt guidance to Kit.
type App interface {
	// Name returns the unique app identifier (e.g. "todo").
	Name() string

	// SystemPrompt returns text appended to the agent system prompt.
	// Return "" if the app has no prompt contribution.
	SystemPrompt() string

	// ToolMetas returns shared tool metadata for both agent and MCP.
	ToolMetas() []services.ToolMeta

	// RegisterAgentTools adds this app's tools to the agent registry.
	// The registerer is *tools.Registry but declared as any to avoid import cycles.
	RegisterAgentTools(registerer any, isAdmin bool)

	// RegisterMCPTools returns MCP tools for the given caller.
	RegisterMCPTools(pool *pgxpool.Pool, svc *services.Services, caller *services.Caller) []mcpserver.ServerTool

	// RegisterRoutes adds HTTP routes (convention: /apps/{name}/...).
	RegisterRoutes(mux *http.ServeMux)

	// CronJobs returns periodic jobs this app needs. Nil if none.
	CronJobs() []CronJob
}

// CronJob defines a periodic background task for an app.
type CronJob struct {
	Name     string
	Interval time.Duration
	Run      func(ctx context.Context, pool *pgxpool.Pool, enc *crypto.Encryptor) error
}

// Global registry — apps self-register via init().
var registry []App

// Register adds an app to the global registry. Called from app init() functions.
func Register(a App) {
	registry = append(registry, a)
}

// All returns all registered apps.
func All() []App {
	return registry
}

// Init lets all registered apps initialize their services after DB is ready.
func Init(pool *pgxpool.Pool) {
	for _, a := range registry {
		if initer, ok := a.(interface{ Init(pool *pgxpool.Pool) }); ok {
			initer.Init(pool)
		}
	}
}

// RegisterAllRoutes registers HTTP routes for all apps on the given mux.
func RegisterAllRoutes(mux *http.ServeMux) {
	for _, a := range registry {
		a.RegisterRoutes(mux)
	}
}

// BuildMCPTools builds MCP tools from all apps for a given caller.
func BuildMCPTools(pool *pgxpool.Pool, svc *services.Services, caller *services.Caller) []mcpserver.ServerTool {
	var allTools []mcpserver.ServerTool
	for _, a := range registry {
		allTools = append(allTools, a.RegisterMCPTools(pool, svc, caller)...)
	}
	return allTools
}

// SystemPrompts returns concatenated system prompt contributions from all apps.
func SystemPrompts() string {
	var b strings.Builder
	for _, a := range registry {
		if p := a.SystemPrompt(); p != "" {
			b.WriteString("\n\n")
			b.WriteString(p)
		}
	}
	return b.String()
}

// MCPToolFromMeta creates an mcpserver.ServerTool from a ToolMeta and handler.
func MCPToolFromMeta(meta services.ToolMeta, handler mcpserver.ToolHandlerFunc) mcpserver.ServerTool {
	schemaJSON, _ := json.Marshal(meta.Schema)
	return mcpserver.ServerTool{
		Tool:    mcp.NewToolWithRawSchema(meta.Name, meta.Description, schemaJSON),
		Handler: handler,
	}
}
