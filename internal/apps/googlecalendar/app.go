// Package googlecalendar is the reusable Google Calendar integration for
// Kit. A tenant stores a service-account JSON key + target calendar id in
// the integrations substrate; the package mints access tokens and exposes a
// write-capable client (LoadClient). It's infrastructure, not a user-facing
// feature: no DescribableApp (absent from the feature-toggle list), no
// CardProvider (absent from the swipe UI). First consumer: the Square shift
// → calendar sync. Auth is a service account (not user OAuth): share the
// target calendar with the service account's email as a writer — no token
// to expire, no consent screen, no cost.
package googlecalendar

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/integrations"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

var instance *App

func init() {
	instance = &App{}
	apps.Register(instance)
	integrations.RegisterTypeSpec(typeSpec())
}

// App is the Google Calendar integration app.
type App struct {
	pool *pgxpool.Pool
	enc  *crypto.Encryptor
}

// Instance returns the singleton so consumer packages (the shift sync) can
// call LoadClient.
func Instance() *App { return instance }

// Init caches the pool. Called by apps.Init().
func (a *App) Init(pool *pgxpool.Pool) { a.pool = pool }

// Configure wires the encryptor and registers the admin integration card.
func Configure(enc *crypto.Encryptor) {
	if instance == nil {
		return
	}
	instance.enc = enc
}

func (a *App) Name() string { return "google_calendar" }

// No DisplayName/Description: infrastructure, not a feature app.

func (a *App) SystemPrompt() string { return "" }

func (a *App) ToolMetas() []services.ToolMeta { return googleCalendarTools }

func (a *App) RegisterAgentTools(_ context.Context, registerer any, caller *services.Caller, isAdmin bool) {
	r, ok := registerer.(*tools.Registry)
	if !ok || !isAdmin || caller == nil {
		return
	}
	registerGoogleCalendarAgentTools(r, a)
}

func (a *App) RegisterMCPTools(_ *pgxpool.Pool, _ *services.Services) []mcpserver.ServerTool {
	return buildGoogleCalendarMCPTools(a)
}

func (a *App) RegisterRoutes(_ apps.Mux) {}

// typeSpec declares the integrations TypeSpec for
// ("google_calendar", "service_account"). Tenant-scoped: the admin pastes
// the service-account JSON key and the target calendar id.
func typeSpec() integrations.TypeSpec {
	return integrations.TypeSpec{
		Provider:    Provider,
		AuthType:    AuthType,
		DisplayName: "Google Calendar",
		Description: "Write events into an existing team calendar via a Google service account. Create a service account, download its JSON key, then share the calendar with the service account's email as a writer.",
		Scope:       integrations.ScopeTenant,
		Fields: []integrations.FieldSpec{
			{Name: "service_account_json", Label: "Service account JSON key", InputType: "textarea", Target: integrations.TargetPrimaryToken, Required: true, Help: "The full JSON key file for the Google service account."},
			{Name: "calendar_id", Label: "Target calendar ID", InputType: "text", Target: integrations.TargetConfig, Required: true, Help: "The calendar to write into (e.g. the email-like id from the calendar's settings, or 'primary')."},
		},
	}
}
