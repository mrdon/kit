package apps

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps/cards/shared"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
)

// AgentToolRegisterer is satisfied by *tools.Registry.
// Defined here to avoid an import cycle between apps and tools.
type AgentToolRegisterer interface {
	RegisterDef(name, description string, schema map[string]any, adminOnly bool, handler any)
}

// Mux is the subset of *http.ServeMux that apps use to register routes.
// RegisterAllRoutes passes a per-tenant enablement-gating wrapper instead of
// the raw mux so disabling an app cuts off every route it contributes.
// *http.ServeMux satisfies this directly.
type Mux interface {
	Handle(pattern string, handler http.Handler)
	HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request))
}

// DescribableApp is an optional interface an App can implement to provide
// human-facing metadata for the admin apps-settings UI. Apps that don't
// implement it fall back to a title-cased Name() with no description.
type DescribableApp interface {
	DisplayName() string
	Description() string
}

// UsageReporter is an optional interface a feature app can implement to show an
// admin a one-line, tenant-scoped summary of how much it's being used (e.g.
// "8 secrets", "3 open tasks") — so they can judge whether disabling it is
// safe. Keep the implementation to a single cheap, indexed count: it runs
// synchronously when the Apps settings page loads. Return "" for "nothing yet".
type UsageReporter interface {
	Usage(ctx context.Context, tenantID uuid.UUID) (string, error)
}

// CountLabel formats a count with a singular/plural unit for usage summaries:
// CountLabel(0, "secret", "secrets") == "No secrets"; CountLabel(1, ...) ==
// "1 secret"; CountLabel(8, ...) == "8 secrets". Always non-empty, so a usage
// reporter that returns it always shows a line (an empty AppUsage means the app
// doesn't report usage at all, and the UI shows nothing).
func CountLabel(n int, singular, plural string) string {
	switch {
	case n <= 0:
		return "No " + plural
	case n == 1:
		return "1 " + singular
	default:
		return fmt.Sprintf("%d %s", n, plural)
	}
}

// AppUsage returns a feature app's one-line usage summary for the tenant, or ""
// if the app doesn't report usage. Errors are logged and swallowed — usage is
// advisory chrome and must never fail the Apps page.
func AppUsage(ctx context.Context, tenantID uuid.UUID, name string) string {
	for _, a := range registry {
		if a.Name() != name {
			continue
		}
		r, ok := a.(UsageReporter)
		if !ok {
			return ""
		}
		summary, err := r.Usage(ctx, tenantID)
		if err != nil {
			slog.Warn("app usage report failed", "app", name, "error", err)
			return ""
		}
		return summary
	}
	return ""
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

	// RegisterRoutes adds HTTP routes. Conventions: SPA pages live in the
	// React console at /{slug}/web/...; JSON APIs at /{slug}/api/<app>/...;
	// /{slug}/apps/<app>/... is reserved for non-page, non-data endpoints
	// (OAuth/redirect bridges, binary serves). The prefix isn't enforced —
	// each app builds its own paths and middleware chain.
	//
	// The mux is a per-tenant enablement gate (see RegisterAllRoutes): any
	// pattern containing {slug} is automatically 404'd for tenants that have
	// disabled this app. Public, tenant-less routes (no {slug}) pass through
	// ungated — apps that serve those must enforce enablement themselves once
	// they resolve a tenant (e.g. the widget from its token).
	RegisterRoutes(mux Mux)

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

// Init lets all registered apps initialize their services after DB is ready,
// and wires the per-tenant enablement gate.
func Init(pool *pgxpool.Pool) {
	initEnablement(pool)
	for _, a := range registry {
		if initer, ok := a.(interface{ Init(pool *pgxpool.Pool) }); ok {
			initer.Init(pool)
		}
	}
}

// RegisterAllRoutes registers HTTP routes for all apps on the given mux.
// Toggleable feature apps get their routes wrapped in a gatingMux so that
// {slug}-scoped patterns are 404'd for tenants that have disabled the app —
// guaranteeing every route the app registers (now or later) honours enablement
// without per-app changes. Core infrastructure apps register on the raw mux.
func RegisterAllRoutes(mux *http.ServeMux, pool *pgxpool.Pool) {
	for _, a := range registry {
		if !IsToggleable(a.Name()) {
			a.RegisterRoutes(mux)
			continue
		}
		a.RegisterRoutes(&gatingMux{real: mux, pool: pool, appName: a.Name()})
	}
}

// gatingMux wraps a real mux. For patterns that carry a {slug} (tenant-scoped),
// it interposes a handler that resolves the tenant and returns 404 when the app
// is disabled for that tenant. Tenant-less patterns are registered unchanged.
type gatingMux struct {
	real    *http.ServeMux
	pool    *pgxpool.Pool
	appName string
}

func (g *gatingMux) Handle(pattern string, handler http.Handler) {
	g.real.Handle(pattern, g.gate(pattern, handler))
}

func (g *gatingMux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	g.real.Handle(pattern, g.gate(pattern, http.HandlerFunc(handler)))
}

func (g *gatingMux) gate(pattern string, next http.Handler) http.Handler {
	if !strings.Contains(pattern, "{slug}") {
		return next // tenant-less route: can't resolve a tenant here, pass through
	}
	appName := g.appName
	pool := g.pool
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slug := r.PathValue("slug")
		tenant, err := models.GetTenantBySlug(r.Context(), pool, slug)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		// Unknown slug falls through to the handler's own middleware, which
		// 404s consistently. Only a known-but-disabled tenant is gated here.
		if tenant != nil && !IsEnabled(r.Context(), tenant.ID, appName) {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// BuildMCPTools builds MCP tools from all apps. Tools are caller-agnostic
// at registration — each handler resolves the caller from ctx per request.
// It also returns a toolName→appName map so the MCP server can hide and reject
// an app's tools for tenants that have disabled it (discovery + call-time).
func BuildMCPTools(pool *pgxpool.Pool, svc *services.Services) ([]mcpserver.ServerTool, map[string]string) {
	slog.Info("building app MCP tools", "registered_apps", len(registry))
	var allTools []mcpserver.ServerTool
	appByTool := make(map[string]string)
	for _, a := range registry {
		appTools := a.RegisterMCPTools(pool, svc)
		toolNames := make([]string, len(appTools))
		for i, t := range appTools {
			toolNames[i] = t.Tool.Name
			appByTool[t.Tool.Name] = a.Name()
		}
		slog.Info("app MCP tools registered",
			"app", a.Name(),
			"tool_count", len(appTools),
			"tools", toolNames,
		)
		allTools = append(allTools, appTools...)
	}
	return allTools, appByTool
}

// SystemPromptsFor returns concatenated system prompt contributions from every
// app enabled for the given tenant. Disabled apps contribute nothing, so the
// agent is never told about tools it can't call.
func SystemPromptsFor(ctx context.Context, tenantID uuid.UUID) string {
	var b strings.Builder
	for _, a := range registry {
		if !IsEnabled(ctx, tenantID, a.Name()) {
			continue
		}
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
