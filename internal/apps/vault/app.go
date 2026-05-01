package vault

import (
	"context"
	_ "embed"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

// (CardSurface, CardCreateInput, etc. are defined below; the App struct
// holds an optional CardSurface populated via Configure.)

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
	pool   *pgxpool.Pool
	svc    *Service
	cards  CardSurface
	signer *auth.SessionSigner
}

// CardSurface is the small slice of CardService the vault needs.
// Declared as an interface so tests can swap in a no-op without pulling
// cards in. CreateDecision handles admin-targeted grant requests + the
// failed-unlock alarm; CreateBriefing handles the user-targeted
// security-tripwire notifications (reset triggered, access granted).
type CardSurface interface {
	CreateDecision(ctx context.Context, c *services.Caller, in CardCreateInput) error
	CreateBriefing(ctx context.Context, c *services.Caller, in CardCreateInput) error
}

// CardCreateInput is the projection of cards.CardCreateInput we use.
// cmd/kit/vault_cards.go translates this into a cards.CardCreateInput.
// RoleScopes targets a role pool (e.g. {"admin"} for grant requests);
// UserScopes targets specific users (used for security tripwires —
// reset-triggered briefings, access-granted briefings, failed-unlock
// decisions).
type CardCreateInput struct {
	Title      string
	Body       string
	RoleScopes []string
	UserScopes []uuid.UUID

	Decision *CardDecisionCreateInput // populated for CreateDecision
	Briefing *CardBriefingCreateInput // populated for CreateBriefing
}

type CardDecisionCreateInput struct {
	Priority            string
	RecommendedOptionID string
	Options             []CardDecisionOption
}

type CardDecisionOption struct {
	OptionID  string
	Label     string
	ToolName  string
	Arguments []byte
}

type CardBriefingCreateInput struct {
	Severity string // "info" | "notable" | "important"
}

// Init wires the service after the DB pool is available. Called by
// apps.Init from cmd/kit/main.go after migrations succeed.
func (a *App) Init(pool *pgxpool.Pool) {
	a.pool = pool
	a.svc = NewService(pool)
}

// Configure wires the surfaces the vault uses at runtime:
//   - cards: card creation (admin-targeted grant decisions + user-
//     targeted security-tripwire briefings + failed-unlock decision)
//   - signer: session cookie middleware on HTTP routes
//
// cards is nil-safe in tests (events just don't fire). HTTP routes
// refuse to register without a signer.
func Configure(cards CardSurface, signer *auth.SessionSigner) {
	if instance == nil {
		return
	}
	instance.cards = cards
	instance.signer = signer
	if instance.svc != nil {
		instance.svc.cards = cards
	}
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
