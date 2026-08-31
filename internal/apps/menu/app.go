package menu

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
const AppName = "menu"

var instance *App

func init() {
	instance = &App{}
	apps.Register(instance)
}

// App is the menu-board feature app.
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
// to render a board's copyable public link — the string an admin pastes into
// a kiosk board.
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
func (a *App) DisplayName() string { return "Menu boards" }
func (a *App) Description() string {
	return "Render a tap list as a full-screen page and serve it at a URL a kiosk can show."
}

// SystemPrompt tells the agent the one thing it cannot work out from the tool
// schemas: that publishing a board and putting it on a screen are two steps,
// and this app only does the first.
func (a *App) SystemPrompt() string {
	return "Menu boards render a tap list at a public URL. Publishing a board does " +
		"not put it on a screen: hand the returned URL to whoever manages the kiosk " +
		"boards, or set it as a kiosk board's URL, before saying a screen has changed."
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
	return apps.CountLabel(n, "board", "boards"), nil
}

// RegisterRoutes mounts the public board page and the console's read-only API.
func (a *App) RegisterRoutes(mux apps.Mux) {
	if a.pool == nil || a.svc == nil {
		return
	}
	registerPublicRoutes(mux, a)
	if a.signer != nil {
		registerConsoleRoutes(mux, a)
	}
}

func (a *App) ToolMetas() []services.ToolMeta { return toolMetas() }

func (a *App) RegisterAgentTools(ctx context.Context, registerer any, caller *services.Caller, isAdmin bool) {
	registerAgentTools(ctx, registerer, caller, isAdmin, a)
}

func (a *App) RegisterMCPTools(pool *pgxpool.Pool, svc *services.Services) []mcpserver.ServerTool {
	return registerMCPTools(pool, svc, a)
}
