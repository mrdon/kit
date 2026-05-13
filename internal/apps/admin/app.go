// Package admin hosts admin-only dashboard pages that span multiple
// other apps. v1 ships a single page — the integrations index at
// /{slug}/admin/integrations — that surfaces every registered
// Integration so admins can find Netlify, GitHub, and future
// connection surfaces from one place. Apps surface themselves via
// RegisterIntegration; admin imports nothing from them.
package admin

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/services"
)

var instance *App

func init() {
	instance = &App{}
	apps.Register(instance)
}

// App owns the per-tenant admin dashboard. Currently just the
// integrations index; will grow over time.
type App struct {
	pool   *pgxpool.Pool
	signer *auth.SessionSigner
}

// Init wires the pool. Called after migrations succeed.
func (a *App) Init(pool *pgxpool.Pool) {
	a.pool = pool
}

// Configure wires the runtime surfaces.
func Configure(signer *auth.SessionSigner) {
	if instance == nil {
		return
	}
	instance.signer = signer
}

func (a *App) Name() string { return "admin" }

func (a *App) SystemPrompt() string { return "" }

func (a *App) ToolMetas() []services.ToolMeta { return nil }

func (a *App) RegisterAgentTools(_ context.Context, _ any, _ *services.Caller, _ bool) {
}

func (a *App) RegisterMCPTools(_ *pgxpool.Pool, _ *services.Services) []mcpserver.ServerTool {
	return nil
}

func (a *App) RegisterRoutes(mux *http.ServeMux) {
	if a.pool == nil || a.signer == nil {
		return
	}
	registerAdminRoutes(mux, a)
}

func (a *App) CronJobs() []apps.CronJob { return nil }
