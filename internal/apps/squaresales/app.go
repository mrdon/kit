// Package squaresales turns a tenant's Square sales into a daily briefing
// card, with the analysis done in Go rather than by an agent.
//
// It replaces an LLM-driven "daily recap" job that called tools to gather
// its own data. That job silently produced nothing on 25% of days -- the
// model ended its turn without calling a single tool -- and cost tokens on
// the days it worked. Everything interesting here is arithmetic over
// rollups: a same-weekday median, a robust z-score, a few thresholds. Go
// computes it, formats it, and posts the card through cards.
// CreateSystemBriefing. No agent, no prompt, no tokens, and it cannot
// silently skip a step.
//
// It consumes internal/apps/square for the Reporting API client and owns
// three rollup tables of its own. DescribableApp makes it toggleable per
// tenant; AppliesTo gates it off for tenants with no Square integration.
package squaresales

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

// AppName is the registry name + enablement key.
const AppName = "square_sales"

var instance *App

func init() {
	instance = &App{}
	apps.Register(instance)
}

// App is the Square sales rollup + daily card feature.
type App struct {
	pool  *pgxpool.Pool
	cards CardSurface
}

// Init caches the pool and declares the recurring work. Called by apps.Init().
func (a *App) Init(pool *pgxpool.Pool) {
	a.pool = pool
	a.registerScheduledTasks()
}

// Configure wires the card surface. Kept as an injected interface rather
// than an import so the dependency graph stays one-way: cards never imports
// squaresales, and squaresales never imports cards. cmd/kit adapts the
// concrete CardService — the same shape the vault app uses.
func Configure(surface CardSurface) {
	if instance == nil {
		return
	}
	instance.cards = surface
}

func (a *App) Name() string { return AppName }

// DisplayName / Description make this a toggleable feature app in admin
// settings (the square plumbing package deliberately omits these).
func (a *App) DisplayName() string { return "Square Sales Insights" }
func (a *App) Description() string {
	return "Post a daily card with yesterday's Square sales compared against a same-weekday baseline, flagging unusual days, dead stretches and item movers."
}

// SystemPrompt is empty by design: this app ships no prose to an LLM.
func (a *App) SystemPrompt() string { return "" }

func (a *App) ToolMetas() []services.ToolMeta { return squareSalesTools }

func (a *App) RegisterAgentTools(_ context.Context, registerer any, caller *services.Caller, isAdmin bool) {
	r, ok := registerer.(*tools.Registry)
	if !ok || !isAdmin || caller == nil {
		return
	}
	registerSquareSalesAgentTools(r, a)
}

func (a *App) RegisterMCPTools(_ *pgxpool.Pool, _ *services.Services) []mcpserver.ServerTool {
	return buildSquareSalesMCPTools(a)
}

// RegisterRoutes is a no-op: the card is the whole user surface, and the
// two admin tools cover operations. There is no console page to serve.
func (a *App) RegisterRoutes(_ apps.Mux) {}
