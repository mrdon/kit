// Package trivia is a live pub quiz: Jeopardy's board married to Wits &
// Wagers' round mechanic, running on three surfaces at once -- a host console
// in the admin web UI, a phone in every team's hand, and a big TV the whole
// room is watching.
//
// Everyone answers every question, all guesses are revealed together, then
// everyone bets on whose guess is closest. Answers are numeric, so "closest
// without going over" scores itself: no buzzer race, no team sitting idle, no
// host adjudication.
//
// The host drives the beats (pick a cell, reveal, next) but is never an
// authority: phase deadlines are absolute server timestamps, so closing the
// host's laptop mid-question does not stop the clock. All game state lives in
// Postgres, which is what lets the process restart mid-round and lets a phone
// that dropped off bar wifi resync by reading one snapshot.
package trivia

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/redis/go-redis/v9"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

// AppName is the registry identifier, used for enablement and the URL
// segment. Verified not to collide with any currently-claimed slug-level
// path segment.
const AppName = "trivia"

var instance *App

func init() {
	instance = &App{}
	apps.Register(instance)
}

// App is the trivia feature app.
type App struct {
	pool    *pgxpool.Pool
	svc     *Service
	signer  *auth.SessionSigner
	baseURL string
}

// Init builds the service once the pool exists. Called by apps.Init.
func (a *App) Init(pool *pgxpool.Pool) {
	a.pool = pool
	a.svc = NewService(pool)
	a.registerScheduledTasks()
}

// Configure wires the console session signer, the external base URL used to
// build the join link a phone scans off the TV, and the optional Redis client
// that relays live snapshots between web processes.
//
// rdb may be nil: with no Redis the relay is simply absent, which is exactly
// correct at one web process and degrades to per-process fan-out plus the
// clients' poll fallback at two. Nothing here requires Redis to be up.
func Configure(signer *auth.SessionSigner, baseURL string, rdb *redis.Client) {
	if instance == nil {
		return
	}
	instance.signer = signer
	instance.baseURL = baseURL
	if instance.svc != nil {
		instance.svc.ConfigureRelay(rdb)
	}
}

func (a *App) Name() string { return AppName }

// DisplayName and Description feed the admin Apps settings page -- and
// implementing DescribableApp is what makes this app toggleable per tenant,
// which is what 404s every public game URL when a workspace turns it off.
func (a *App) DisplayName() string { return "Trivia" }
func (a *App) Description() string {
	return "Run a live pub quiz on the TV, with every team playing from their phone."
}

// SystemPrompt is deliberately empty. Nothing about "pick cell 3, reveal now"
// is improved by routing through an LLM in a loud bar, and a mis-fired tool
// during a live game is destructive and unrecoverable.
func (a *App) SystemPrompt() string { return "" }

// RegisterRoutes mounts the host console API and the two public surfaces.
//
// Every pattern carries {slug}, so the enablement gate in
// apps.RegisterAllRoutes 404s all of them for a workspace that has turned
// trivia off -- including the TV and the phones, which is the behaviour an
// admin expects from that switch.
func (a *App) RegisterRoutes(mux apps.Mux) {
	if a.pool == nil || a.svc == nil || a.signer == nil {
		return
	}
	registerConsoleRoutes(mux, a)
	registerPublicRoutes(mux, a)
}

func (a *App) ToolMetas() []services.ToolMeta { return triviaTools }

// RegisterAgentTools adds the two read-only tools. Both surfaces run the same
// dispatchCore, so they cannot produce different text for the same question.
func (a *App) RegisterAgentTools(_ context.Context, registerer any, _ *services.Caller, _ bool) {
	reg, ok := registerer.(*tools.Registry)
	if !ok || a.pool == nil || a.svc == nil {
		return
	}
	registerAgentTools(reg, a.pool, a.svc)
}

func (a *App) RegisterMCPTools(pool *pgxpool.Pool, _ *services.Services) []mcpserver.ServerTool {
	if a.svc == nil {
		return nil
	}
	return buildMCPTools(pool, a.svc)
}

// Usage reports the question bank's size for the Apps settings page. The bank
// is what a workspace would lose by turning the app off -- games are one
// night, but a bank is grown over months.
func (a *App) Usage(ctx context.Context, tenantID uuid.UUID) (string, error) {
	if a.pool == nil {
		return "", nil
	}
	n, err := CountQuestions(ctx, a.pool, tenantID)
	if err != nil {
		return "", err
	}
	return apps.CountLabel(n, "question", "questions"), nil
}

// StartLive brings up the process-wide live machinery: the cross-process
// snapshot relay and the deadline sweeper. Called once from main after the
// app is configured, and shut down with the passed context.
func StartLive(ctx context.Context) {
	if instance == nil || instance.svc == nil {
		return
	}
	instance.svc.StartRelay(ctx)
	instance.svc.StartSweeper(ctx)
}
