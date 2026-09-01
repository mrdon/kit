// Package square is the reusable Square integration for Kit. It stores a
// tenant's Square OAuth tokens in the integrations substrate and exposes an
// authenticated client (LoadClient) plus read tools. It is infrastructure,
// not a user-facing feature: it implements neither DescribableApp (so it's
// absent from the admin feature-toggle list) nor CardProvider (so it never
// shows in the end-user swipe UI). Consumers today: the shift → calendar
// sync and the daily sales card. Config is admin-only via the
// integrations index (admin.RegisterIntegration) + the hosted setup form.
package square

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

// App is the Square integration app. It holds the DB pool, encryptor, and
// the app-level OAuth credentials used to refresh per-tenant tokens.
type App struct {
	pool         *pgxpool.Pool
	enc          *crypto.Encryptor
	clientID     string
	clientSecret string
	apiBase      string
}

// Instance returns the singleton app so consumer packages (e.g. the shift
// sync) can call LoadClient / ListPublishedShifts.
func Instance() *App { return instance }

// Init caches the pool. Called by apps.Init().
func (a *App) Init(pool *pgxpool.Pool) { a.pool = pool }

// Configure wires the encryptor and Square OAuth app credentials, and
// registers the admin integration card. apiBase is derived from the
// environment ("sandbox" → the sandbox host). Called once from main.go.
func Configure(enc *crypto.Encryptor, applicationID, applicationSecret, environment string) {
	if instance == nil {
		return
	}
	instance.enc = enc
	instance.clientID = applicationID
	instance.clientSecret = applicationSecret
	instance.apiBase = prodAPIBase
	if environment == "sandbox" {
		instance.apiBase = sandboxAPIBase
	}
}

func (a *App) Name() string { return "square" }

// No DisplayName/Description: square is infrastructure, not a feature app.

func (a *App) SystemPrompt() string { return "" }

func (a *App) ToolMetas() []services.ToolMeta { return squareTools }

// RegisterAgentTools registers the read tools only for admins with a
// configured Square integration — an unconfigured tenant gets no tools and
// routes through the configure_integration flow instead.
func (a *App) RegisterAgentTools(ctx context.Context, registerer any, caller *services.Caller, isAdmin bool) {
	r, ok := registerer.(*tools.Registry)
	if !ok || !isAdmin || caller == nil {
		return
	}
	registerSquareAgentTools(r, a)
}

func (a *App) RegisterMCPTools(_ *pgxpool.Pool, _ *services.Services) []mcpserver.ServerTool {
	return buildSquareMCPTools(a)
}

func (a *App) RegisterRoutes(_ apps.Mux) {}

// typeSpec declares the integrations TypeSpec for ("square", "oauth2").
// Tenant-scoped, single connection per workspace. For v1 the admin pastes
// an access + refresh token obtained once via Square's OAuth; a dedicated
// connect route can replace the paste later. The app-level client_id /
// client_secret used to refresh come from env config, not this form.
func typeSpec() integrations.TypeSpec {
	return integrations.TypeSpec{
		Provider:    Provider,
		AuthType:    AuthType,
		DisplayName: "Square",
		Description: "Connect Square to sync the published staff schedule and pull daily sales. Needs scopes TIMECARDS_READ, EMPLOYEES_READ, MERCHANT_PROFILE_READ and REPORTING_READ. Simplest: paste your app's non-expiring Production Access Token (Developer Console → Credentials → Production), which carries every scope, and leave the refresh token blank.",
		Scope:       integrations.ScopeTenant,
		Fields: []integrations.FieldSpec{
			{Name: "access_token", Label: "Access token", InputType: "password", Target: integrations.TargetPrimaryToken, Required: true, Help: "A Square Production Access Token, or an OAuth access token."},
			{Name: "refresh_token", Label: "Refresh token (optional)", InputType: "password", Target: integrations.TargetSecondaryToken, Required: false, Help: "Only for expiring OAuth tokens — Kit uses it to auto-renew. Leave blank for a non-expiring personal access token."},
		},
	}
}
