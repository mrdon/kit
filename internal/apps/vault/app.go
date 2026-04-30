package vault

import (
	"context"
	_ "embed"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

var instance *App

func init() {
	instance = &App{}
	apps.Register(instance)
}

// App is the password-vault feature for Kit. See package doc for the full
// trust model. The app exposes:
//   - HTTP routes under /{slug}/apps/vault/... for register, unlock, add,
//     reveal, and grant flows (all authenticated via the session cookie).
//   - Agent + MCP tools for find/list/view/add/scope-update/delete that
//     return URLs (never values) and apply the per-user/role authz filter.
//   - Decision cards for tenant admins on grant requests / password resets.
//   - Briefings for the user being acted on (security tripwires).
type App struct {
	pool *pgxpool.Pool
	svc  *Service
}

// Init wires the service after the DB pool is available. Called by
// apps.Init from cmd/kit/main.go after migrations succeed.
func (a *App) Init(pool *pgxpool.Pool) {
	a.pool = pool
	a.svc = NewService(pool)
}

// Service returns the live vault service, or nil before Init has run.
// Exposed for tests + for the MCP layer to share the same instance.
func (a *App) Service() *Service { return a.svc }

func (a *App) Name() string { return "vault" }

//go:embed prompts/system_vault.tmpl
var systemPromptText string

func (a *App) SystemPrompt() string { return systemPromptText }

func (a *App) ToolMetas() []services.ToolMeta { return vaultToolMetas }

func (a *App) RegisterAgentTools(_ context.Context, registerer any, _ *services.Caller, isAdmin bool) {
	r, ok := registerer.(*tools.Registry)
	if !ok || a.svc == nil {
		return
	}
	registerVaultAgentTools(r, isAdmin, a.svc)
}

func (a *App) RegisterMCPTools(_ *pgxpool.Pool, _ *services.Services) []mcpserver.ServerTool {
	if a.svc == nil {
		return nil
	}
	return buildVaultMCPTools(a.svc)
}

func (a *App) RegisterRoutes(mux *http.ServeMux) {
	if a.svc == nil || a.pool == nil {
		return
	}
	registerVaultRoutes(mux, a)
}

func (a *App) CronJobs() []apps.CronJob { return nil }
