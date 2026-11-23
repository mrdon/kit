// Package hoppers is a frog race for a bar, running on three surfaces at
// once: the TV on the wall draws the race, every player flicks their own frog
// from their phone, and the host moves the night on from the console or the
// taproom iPad.
//
// The rules live in the pure engine (./engine), which also runs headless in
// cmd/hopsim. This package is everything around it: the game row the engine
// is saved in, the row-locked transaction every change runs in, the
// heartbeats that tell the engine who is still in the room, and the live
// transport it shares with trivia (internal/livegame).
package hoppers

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/redis/go-redis/v9"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/services"
)

// AppName is the registry identifier, used for enablement and the URL
// segment (/{slug}/hoppers/...).
const AppName = "hoppers"

var instance *App

func init() {
	instance = &App{}
	apps.Register(instance)
}

// App is the Hoppers feature app.
type App struct {
	pool    *pgxpool.Pool
	svc     *Service
	signer  *auth.SessionSigner
	baseURL string
}

// Init records the pool. The service is built in Configure, once the base
// URL the join links are made from is known.
func (a *App) Init(pool *pgxpool.Pool) {
	a.pool = pool
}

// Configure wires the console session signer, the external base URL the
// join link is built from, and the optional Redis client that relays live
// snapshots between web processes (nil is fine at one process).
func Configure(signer *auth.SessionSigner, baseURL string, rdb *redis.Client) {
	if instance == nil || instance.pool == nil {
		return
	}
	instance.signer = signer
	instance.baseURL = baseURL
	instance.svc = NewService(instance.pool, baseURL)
	instance.svc.ConfigureRelay(rdb)
}

// StartLive brings up the relay and the game clock. Called once from main
// after Configure, and shut down with ctx.
func StartLive(ctx context.Context) {
	if instance == nil || instance.svc == nil {
		return
	}
	instance.svc.relay.Start(ctx)
	instance.svc.StartSweeper(ctx)
}

func (a *App) Name() string { return AppName }

// DisplayName and Description make the app toggleable per workspace;
// switching it off 404s every Hoppers URL, TV and phones included.
func (a *App) DisplayName() string { return "Hoppers" }
func (a *App) Description() string {
	return "Run a frog race on the TV, with everyone flicking their frog from their phone."
}

// SystemPrompt is empty: Hoppers has no agent tools yet, and a live race is
// run from the host screen, not through chat.
func (a *App) SystemPrompt() string { return "" }

// RegisterRoutes mounts the host's console API and the public surfaces.
// Every pattern but the short join link carries {slug}, so the enablement
// gate covers them.
func (a *App) RegisterRoutes(mux apps.Mux) {
	if a.pool == nil || a.svc == nil || a.signer == nil {
		return
	}
	registerConsoleRoutes(mux, a)
	registerPublicRoutes(mux, a)
}

func (a *App) ToolMetas() []services.ToolMeta { return nil }

func (a *App) RegisterAgentTools(context.Context, any, *services.Caller, bool) {}

func (a *App) RegisterMCPTools(*pgxpool.Pool, *services.Services) []mcpserver.ServerTool {
	return nil
}
