package apps

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps/cards/shared"
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
	//
	// ctx and caller are per-session so apps can gate registration on
	// runtime state (e.g. the email app hides its tools when the caller
	// has no email integration configured). Both may be nil in test paths
	// that build a registry without a caller; apps should no-op safely.
	RegisterAgentTools(ctx context.Context, registerer any, caller *services.Caller, isAdmin bool)

	// RegisterMCPTools returns this app's MCP tools. Handlers resolve the
	// caller from ctx at call time, so tools can be registered once at
	// server startup instead of per session.
	RegisterMCPTools(pool *pgxpool.Pool, svc *services.Services) []mcpserver.ServerTool

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

// CardProvider is an optional interface an App can implement to contribute
// items to the PWA stack. The cards host app (internal/apps/cards) fans out
// across all registered providers at request time.
type CardProvider interface {
	// SourceApp is the stable identifier used in URLs and the compound
	// client key. Usually matches App.Name().
	SourceApp() string

	// StackItems returns one page of items for the caller. cursor is the
	// opaque provider-specific cursor from the previous call (empty = first
	// page). limit is an upper bound; providers may return fewer. Return
	// an empty NextCursor when the provider is exhausted.
	StackItems(ctx context.Context, caller *services.Caller, cursor string, limit int) (shared.StackPage, error)

	// GetItem loads a single item with optional kind-specific extras.
	GetItem(ctx context.Context, caller *services.Caller, kind, id string) (*shared.DetailResponse, error)

	// DoAction executes a named action on an item. Returns an ActionResult
	// describing how the client should reconcile (patch or remove).
	DoAction(ctx context.Context, caller *services.Caller, kind, id, actionID string, params json.RawMessage) (*shared.ActionResult, error)
}

// cardProviders is registered separately from apps so a provider can be
// implemented in a sibling type when wiring is awkward.
var cardProviders []CardProvider

// RegisterCardProvider adds a provider to the stack fan-out.
func RegisterCardProvider(p CardProvider) {
	cardProviders = append(cardProviders, p)
}

// CardProviders returns all registered providers.
func CardProviders() []CardProvider {
	return cardProviders
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

// BuildMCPTools builds MCP tools from all apps. Tools are caller-agnostic
// at registration — each handler resolves the caller from ctx per request.
func BuildMCPTools(pool *pgxpool.Pool, svc *services.Services) []mcpserver.ServerTool {
	slog.Info("building app MCP tools", "registered_apps", len(registry))
	var allTools []mcpserver.ServerTool
	for _, a := range registry {
		appTools := a.RegisterMCPTools(pool, svc)
		toolNames := make([]string, len(appTools))
		for i, t := range appTools {
			toolNames[i] = t.Tool.Name
		}
		slog.Info("app MCP tools registered",
			"app", a.Name(),
			"tool_count", len(appTools),
			"tools", toolNames,
		)
		allTools = append(allTools, appTools...)
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

// RunCronJobs starts a goroutine for each cron job declared by every registered
// app. Each goroutine ticks at the job's Interval until ctx is cancelled. Errors
// and panics from individual runs are logged but never bring the process down.
func RunCronJobs(ctx context.Context, pool *pgxpool.Pool, enc *crypto.Encryptor) {
	for _, a := range registry {
		jobs := a.CronJobs()
		for _, job := range jobs {
			slog.Info("starting app cron job", "app", a.Name(), "job", job.Name, "interval", job.Interval)
			go runCronLoop(ctx, a.Name(), job, pool, enc)
		}
	}
}

func runCronLoop(ctx context.Context, appName string, job CronJob, pool *pgxpool.Pool, enc *crypto.Encryptor) {
	ticker := time.NewTicker(job.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runCronOnce(ctx, appName, job, pool, enc)
		}
	}
}

func runCronOnce(ctx context.Context, appName string, job CronJob, pool *pgxpool.Pool, enc *crypto.Encryptor) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("app cron job panicked", "app", appName, "job", job.Name, "panic", r)
		}
	}()
	if err := job.Run(ctx, pool, enc); err != nil {
		slog.Error("app cron job failed", "app", appName, "job", job.Name, "error", err)
	}
}

// MCPToolFromMeta creates an mcpserver.ServerTool from a ToolMeta and handler.
func MCPToolFromMeta(meta services.ToolMeta, handler mcpserver.ToolHandlerFunc) mcpserver.ServerTool {
	schemaJSON, _ := json.Marshal(meta.Schema)
	return mcpserver.ServerTool{
		Tool:    mcp.NewToolWithRawSchema(meta.Name, meta.Description, schemaJSON),
		Handler: handler,
	}
}
