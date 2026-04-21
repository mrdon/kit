package integrations

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

// defaultTokenTTL is the setup URL's time-to-live. Short enough that a
// leaked URL in browser history / shell scrollback becomes worthless
// quickly; long enough to survive "I'll do it after my meeting."
const defaultTokenTTL = 15 * time.Minute

// instance is the process-wide singleton. init() registers it with the
// apps registry so Init runs once the DB is ready. Configure wires the
// HTTP-level deps (base URL, signing secret, encryptor).
var instance *App

func init() {
	instance = &App{ttl: defaultTokenTTL}
	apps.Register(instance)
}

// App is the integrations feature app — it contributes MCP + agent tools
// that mint signed setup URLs, and HTTP routes for the signed-URL web form.
type App struct {
	pool       *pgxpool.Pool
	enc        *crypto.Encryptor
	baseURL    string
	signSecret string
	ttl        time.Duration
}

// Configure wires the non-DB dependencies needed by the integrations app.
// Call once from main.go after the encryptor and config are loaded. Safe
// to call before or after apps.Init — Init handles the pool separately.
func Configure(enc *crypto.Encryptor, baseURL, signSecret string) {
	if instance == nil {
		return
	}
	instance.enc = enc
	instance.baseURL = baseURL
	instance.signSecret = signSecret
}

// Init caches the pool once it's available. Called by apps.Init().
func (a *App) Init(pool *pgxpool.Pool) {
	a.pool = pool
}

func (a *App) Name() string { return "integrations" }

func (a *App) SystemPrompt() string {
	return `## Integrations
When the user wants to connect an external service (email account, GitHub, Stripe, etc.), don't ask them for credentials in chat. Instead:
1. Call list_integration_types to see what's available.
2. Call configure_integration with the chosen provider + auth_type. Relay the returned URL to the user so they can enter the secret in a browser.
3. When the user says they're done, call check_integration_status with the pending_id to confirm it was saved.
You will never see the actual secret — the web form encrypts it before the LLM ever sees anything.`
}

func (a *App) ToolMetas() []services.ToolMeta {
	return services.IntegrationTools
}

func (a *App) RegisterAgentTools(registerer any, _ bool) {
	r, ok := registerer.(*tools.Registry)
	if !ok {
		return
	}
	registerIntegrationAgentTools(r, a)
}

func (a *App) RegisterMCPTools(_ *pgxpool.Pool, _ *services.Services) []mcpserver.ServerTool {
	return buildMCPTools(a)
}

func (a *App) RegisterRoutes(mux *http.ServeMux) {
	a.registerRoutes(mux)
}

func (a *App) CronJobs() []apps.CronJob { return nil }

// tokenTTL returns the configured TTL with a sane default.
func (a *App) tokenTTL() time.Duration {
	if a.ttl > 0 {
		return a.ttl
	}
	return defaultTokenTTL
}

// tokenSecret returns the HMAC signing secret for setup URLs. Sourced
// from Configure; callers get a zero-length key if not wired (in which
// case token signing and verification both fail, which is what we want).
func (a *App) tokenSecret() string { return a.signSecret }

// buildSetupURL mints the signed URL for a pending integration. Looks up
// the tenant slug via the pool so the URL path resolves through
// auth.TenantFromPath without an extra round-trip.
func (a *App) buildSetupURL(ctx context.Context, p *models.PendingIntegration) (string, error) {
	if a.baseURL == "" {
		return "", errors.New("integrations base URL not configured")
	}
	key := deriveTokenKey(a.tokenSecret())
	if len(key) == 0 {
		return "", errors.New("integrations signing secret not configured")
	}
	slug, err := tenantSlug(ctx, a.pool, p.TenantID)
	if err != nil {
		return "", err
	}
	tok := signToken(key, tokenPayload{
		PendingID: p.ID,
		TenantID:  p.TenantID,
		ExpiresAt: p.ExpiresAt.Unix(),
	})
	return fmt.Sprintf("%s/%s/integrations/setup?token=%s", a.baseURL, slug, tok), nil
}

// tenantSlug fetches the slug for a tenant id. Split out so tests can
// pass a fake pool if needed — the production path always hits the DB.
func tenantSlug(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (string, error) {
	tenant, err := models.GetTenantByID(ctx, pool, tenantID)
	if err != nil {
		return "", fmt.Errorf("loading tenant: %w", err)
	}
	if tenant == nil {
		return "", fmt.Errorf("tenant %s not found", tenantID)
	}
	return tenant.Slug, nil
}
