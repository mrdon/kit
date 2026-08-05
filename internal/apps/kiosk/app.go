package kiosk

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/services"
)

// AppName is the registry identifier, used for enablement and the URL prefix.
const AppName = "kiosk"

var instance *App

func init() {
	instance = &App{}
	apps.Register(instance)
}

// App is the kiosk-screens feature app. It contributes no agent or MCP tools:
// a board is configured from the console and read by a machine over plain
// HTTP, and neither surface benefits from the agent sitting in the middle.
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
}

// Configure wires the console session signer and the external base URL used
// to render each board's copyable public link.
func Configure(signer *auth.SessionSigner, baseURL string) {
	if instance == nil {
		return
	}
	instance.signer = signer
	instance.baseURL = baseURL
}

func (a *App) Name() string { return AppName }

// DisplayName and Description feed the admin Apps settings page — and
// implementing DescribableApp is what makes this app toggleable per tenant.
func (a *App) DisplayName() string { return "Kiosk screens" }
func (a *App) Description() string {
	return "Point unattended screens at a URL and change it later without touching the machine."
}

// SystemPrompt adds nothing: the agent has no part in this app's flow.
func (a *App) SystemPrompt() string { return "" }

func (a *App) ToolMetas() []services.ToolMeta { return nil }

func (a *App) RegisterAgentTools(_ context.Context, _ any, _ *services.Caller, _ bool) {}

func (a *App) RegisterMCPTools(_ *pgxpool.Pool, _ *services.Services) []mcpserver.ServerTool {
	return nil
}

// RegisterRoutes mounts the public board redirect and the admin console API.
func (a *App) RegisterRoutes(mux apps.Mux) {
	if a.pool == nil || a.svc == nil || a.signer == nil {
		return
	}
	registerPublicRoutes(mux, a)
	registerConsoleRoutes(mux, a)
}

// Usage reports the board count for the Apps settings page.
func (a *App) Usage(ctx context.Context, tenantID uuid.UUID) (string, error) {
	if a.pool == nil {
		return "", nil
	}
	n, err := CountBoards(ctx, a.pool, tenantID)
	if err != nil {
		return "", err
	}
	return apps.CountLabel(n, "screen", "screens"), nil
}
