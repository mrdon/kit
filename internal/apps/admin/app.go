// Package admin hosts the cross-app Integration registry (registry.go)
// that lets apps surface themselves on the integrations index without
// admin importing them. The index UI itself now lives in the React
// console at /{slug}/web/admin/integrations (rendered from this registry via
// internal/apps/console); admin only keeps a redirect from the legacy
// /{slug}/admin/integrations URL.
package admin

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/services"
)

var instance *App

func init() {
	instance = &App{}
	apps.Register(instance)
}

// App owns the per-tenant admin surface. Its dashboard pages now live in
// the React console (/{slug}/web); admin keeps the cross-app Integration
// registry (registry.go) plus a redirect from the legacy
// /{slug}/admin/integrations URL into the console.
type App struct {
	pool *pgxpool.Pool
}

// Init wires the pool. Called after migrations succeed.
func (a *App) Init(pool *pgxpool.Pool) {
	a.pool = pool
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
	if a.pool == nil {
		return
	}
	registerAdminRoutes(mux, a)
}

func (a *App) CronJobs() []apps.CronJob { return nil }
