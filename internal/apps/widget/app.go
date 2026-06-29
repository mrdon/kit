// Package widget is Kit's modular feature for the website chat widget.
// It contributes admin + analytics tools (agent + MCP surfaces), the
// Slack-OAuth-gated admin pages where tokens are minted, and the
// unauthenticated public HTTP endpoints (/widget.js, /widget/api/*)
// that visitors hit from an embedding website.
package widget

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/agent"
	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

// instance is the process-wide singleton. init() registers it with
// the apps registry so Init runs once the DB is ready. Configure
// wires the HTTP-level deps (agent, signer, baseURL).
var instance *App

func init() {
	instance = &App{}
	apps.Register(instance)
}

// App is the widget feature app. Holds the pool (set in Init), the
// agent runner + session signer + base URL (set in Configure), and
// the request-scoped rate limiter.
type App struct {
	pool    *pgxpool.Pool
	agent   *agent.Agent
	signer  *auth.SessionSigner
	baseURL string
	limiter *Limiter
}

// Configure wires the non-DB dependencies. Call once from main.go
// after the agent and session signer are constructed.
func Configure(a *agent.Agent, signer *auth.SessionSigner, baseURL string) {
	if instance == nil {
		return
	}
	instance.agent = a
	instance.signer = signer
	instance.baseURL = baseURL
	instance.limiter = NewLimiter()
}

// Init caches the pool once it's available. Called by apps.Init().
func (a *App) Init(pool *pgxpool.Pool) {
	a.pool = pool
}

// Name is the unique app identifier — also drives URL prefixes for
// app-contributed routes that follow the /apps/{name}/ convention.
// The widget's public surface lives at the top level (no slug prefix
// since the tenant is in the token), and its admin surface lives at
// /{slug}/widget — both wired in RegisterRoutes directly.
// AppName is the registry identifier, shared by Name() and the
// per-tenant enablement checks elsewhere in the package.
const AppName = "widget"

func (a *App) Name() string { return AppName }

// DisplayName and Description feed the admin Apps settings page.
func (a *App) DisplayName() string { return "Website widget" }
func (a *App) Description() string {
	return "Embeddable chat widget that answers visitor questions on your site."
}

// SystemPrompt contributes nothing to the global agent prompt — the
// widget agent has its own dedicated system prompt assembled via
// agent.BuildWidgetSystemPrompt, not appended to the Slack one.
func (a *App) SystemPrompt() string { return "" }

// ToolMetas returns both the token-admin and analytics metas so the
// MCP tools/list filter sees them.
func (a *App) ToolMetas() []services.ToolMeta {
	out := make([]services.ToolMeta, 0, len(services.WidgetTokenTools)+len(services.WidgetAnalyticsTools))
	out = append(out, services.WidgetTokenTools...)
	out = append(out, services.WidgetAnalyticsTools...)
	return out
}

// RegisterAgentTools wires the widget-token and widget-analytics
// tools onto the agent registry. Both groups respect AdminOnly via
// the metas themselves.
func (a *App) RegisterAgentTools(_ context.Context, registerer any, _ *services.Caller, isAdmin bool) {
	r, ok := registerer.(*tools.Registry)
	if !ok {
		return
	}
	registerWidgetTokenTools(r, isAdmin)
	registerWidgetAnalyticsTools(r, isAdmin)
}

// RegisterMCPTools wires the same tool surfaces for MCP clients.
func (a *App) RegisterMCPTools(pool *pgxpool.Pool, svc *services.Services) []mcpserver.ServerTool {
	return buildWidgetMCPTools(pool, svc)
}

// RegisterRoutes mounts both the public unauthenticated routes
// (/widget.js, /widget/api/*) and the Slack-OAuth-gated admin routes
// (/{slug}/widget*). Done in one place so main.go doesn't need to
// know the widget package's URL structure.
func (a *App) RegisterRoutes(mux apps.Mux) {
	if a.agent == nil {
		slog.Warn("widget app: agent not configured; routes not registered")
		return
	}
	svc := services.NewWidgetTokenService(a.pool, a.baseURL)
	publicSvc := New(a.pool, a.agent, a.limiter)
	NewHandler(publicSvc).Register(mux)
	NewAdminHandler(a.pool, a.signer, svc, a.baseURL).Register(mux)
}

// CronJobs has nothing to schedule.
func (a *App) CronJobs() []apps.CronJob { return nil }
