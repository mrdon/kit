// Package squareshifts syncs a tenant's published Square staff schedule into
// an existing Google Calendar their team already subscribes to. It's a
// user-facing feature (DescribableApp → toggleable in admin settings) that
// consumes two reusable plumbing integrations: internal/apps/square (the
// schedule source) and internal/apps/googlecalendar (the write target). The
// only surfaces are admin: a 15-minute cron sweep plus sync-now / status
// tools. Run outcomes are recorded to audit_events.
package squareshifts

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

// AppName is the registry name + enablement key.
const AppName = "square_shifts"

var instance *App

func init() {
	instance = &App{}
	apps.Register(instance)
}

// App is the Square → Google Calendar shift-sync feature.
type App struct {
	pool   *pgxpool.Pool
	signer *auth.SessionSigner
}

// Init caches the pool. Called by apps.Init().
func (a *App) Init(pool *pgxpool.Pool) {
	a.pool = pool
	a.registerScheduledTasks()
}

// Configure wires the PWA session signer (for the admin Manage page). The
// sync pulls its clients from the square and googlecalendar singletons, so
// there's nothing else to wire beyond the pool (set in Init). enc is accepted
// to match the wiring convention and reserved for future direct use.
func Configure(_ *crypto.Encryptor, signer *auth.SessionSigner) {
	if instance == nil {
		return
	}
	instance.signer = signer
}

func (a *App) Name() string { return AppName }

// DisplayName / Description make this a toggleable feature app in admin
// settings (the plumbing integrations deliberately omit these).
func (a *App) DisplayName() string { return "Square Shift Sync" }
func (a *App) Description() string {
	return "Sync your published Square staff schedule into a Google Calendar your team subscribes to."
}

func (a *App) SystemPrompt() string { return "" }

func (a *App) ToolMetas() []services.ToolMeta { return squareShiftsTools }

func (a *App) RegisterAgentTools(_ context.Context, registerer any, caller *services.Caller, isAdmin bool) {
	r, ok := registerer.(*tools.Registry)
	if !ok || !isAdmin || caller == nil {
		return
	}
	registerSquareShiftsAgentTools(r, a)
}

func (a *App) RegisterMCPTools(_ *pgxpool.Pool, _ *services.Services) []mcpserver.ServerTool {
	return buildSquareShiftsMCPTools(a)
}

func (a *App) RegisterRoutes(mux apps.Mux) {
	if a.pool == nil || a.signer == nil {
		return
	}
	registerSquareShiftsRoutes(mux, a)
}

// CronJobs is unused: this app declares its recurring work via
// scheduler.RegisterScheduledTask in Init (see schedule.go).
func (a *App) CronJobs() []apps.CronJob { return nil }
