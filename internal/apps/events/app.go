// Package events makes Kit the source of truth for a venue's events -- public
// taproom nights, private bookings, and offsite appearances -- and derives
// everything downstream from those rows: a Google Calendar the team and the
// food partner read, and a JSON feed the website's static-site build consumes
// to generate real event pages.
//
// Three surfaces, like every other feature app: a human web UI, an admin area
// for configuration, and the same operations exposed as agent and MCP tools so
// events can be authored from the console form, from chat, or from an AI
// harness over MCP. All three call the same service methods.
//
// The one invariant worth stating up front: visibility is default-deny.
// Leaking a customer's private booking onto the public website is the worst
// failure this app can produce, so events start private, publishing is an
// explicit act, and exactly one predicate (IsPubliclyVisible) decides what may
// leave the building.
package events

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/attachment"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

// AppName is the registry name + enablement key.
const AppName = "events"

var instance *App

func init() {
	instance = &App{}
	apps.Register(instance)
}

// App is the events feature.
type App struct {
	pool   *pgxpool.Pool
	signer *auth.SessionSigner
	svc    *Service
	// enc decrypts attachment bytes; needed for event posters.
	enc *crypto.Encryptor

	// resolveWriter overrides how the calendar client is obtained. Nil in
	// production; tests set it to inject a fake calendar. See writerFor.
	resolveWriter func(ctx context.Context, tenantID uuid.UUID) (calendarWriter, Settings, error)
}

// Instance exposes the registered app for callers that need it outside the
// registry path (cron wiring, cross-app reads).
func Instance() *App { return instance }

// Init caches the pool and builds the service. Called by apps.Init().
func (a *App) Init(pool *pgxpool.Pool) {
	a.pool = pool
	a.svc = &Service{pool: pool}
	a.installSyncHook()
}

// Configure wires the console session signer. The calendar client comes from
// the googlecalendar singleton at call time, so there is no encryptor to hold
// here; enc is accepted to match the wiring convention used by sibling apps.
func Configure(enc *crypto.Encryptor, signer *auth.SessionSigner) {
	if instance == nil {
		return
	}
	instance.signer = signer
	instance.enc = enc
}

// attachments builds the poster store on demand. Nil when the encryptor was
// never wired, which the handlers report rather than panicking on.
func (a *App) attachments() *attachment.Service {
	if a.pool == nil || a.enc == nil {
		return nil
	}
	return attachment.NewService(a.pool, a.enc)
}

func (a *App) Name() string { return AppName }

// DisplayName / Description make this a toggleable feature app in admin
// settings.
func (a *App) DisplayName() string { return "Events" }
func (a *App) Description() string {
	return "Author events once and sync them to Google Calendar and your website."
}

func (a *App) SystemPrompt() string { return mustRender("system_prompt.tmpl", nil) }

func (a *App) ToolMetas() []services.ToolMeta { return eventsTools }

func (a *App) RegisterAgentTools(_ context.Context, registerer any, _ *services.Caller, isAdmin bool) {
	r, ok := registerer.(*tools.Registry)
	if !ok {
		return
	}
	registerAgentTools(r, a.svc, isAdmin)
}

func (a *App) RegisterMCPTools(_ *pgxpool.Pool, _ *services.Services) []mcpserver.ServerTool {
	return buildMCPTools(a.svc)
}

// RegisterRoutes mounts the public website feed plus the console API. The feed
// authenticates with a bearer token (a site build has no Kit session); the
// console routes carry the usual session + CSRF contract, with configuration
// gated on admin at the middleware rather than in the handler.
func (a *App) RegisterRoutes(mux apps.Mux) {
	if a.pool == nil {
		return
	}
	a.registerFeedRoutes(mux)
	registerConsoleRoutes(mux, a)
	registerSettingsRoutes(mux, a)
}

// CronJobs runs the regular sync every 15 minutes and a slower reconciliation
// every 12 hours. The sync trusts its stored content hash and skips unchanged
// events, which makes it cheap but blind to edits made directly in Google; the
// reconcile pass compares against the calendar's actual state to heal those.
func (a *App) CronJobs() []apps.CronJob {
	return []apps.CronJob{
		{
			Name:     "sync_events",
			Interval: 15 * time.Minute,
			Run: func(ctx context.Context, _ *pgxpool.Pool, _ *crypto.Encryptor) error {
				return a.SyncAllTenants(ctx)
			},
		},
		{
			Name:     "reconcile_events",
			Interval: reconcileInterval,
			Run: func(ctx context.Context, _ *pgxpool.Pool, _ *crypto.Encryptor) error {
				return a.ReconcileAllTenants(ctx)
			},
		},
	}
}
