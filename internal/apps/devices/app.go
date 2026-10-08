// Package devices pairs shared machines -- the trivia laptop, the bar iPad
// -- with Kit without anyone ever signing in on them.
//
// A device opens /{slug}/pair and shows a picture and a short code. An
// admin, signed in on their own phone, opens Devices in the console, taps
// the picture, picks what the device may do, and the device's next poll
// receives a long-lived device session (see internal/auth, phase 2 of
// docs/actor-model.md). From then on the device is an actor with a label
// and a capability list, and the console shell shows it only the screens
// those capabilities name.
package devices

import (
	"context"
	"embed"
	"html/template"

	"github.com/jackc/pgx/v5/pgxpool"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/services"
)

// AppName is the registry identifier.
const AppName = "devices"

//go:embed templates/*.html
var templatesFS embed.FS

var pageTmpl = template.Must(template.ParseFS(templatesFS, "templates/*.html"))

var instance *App

func init() {
	instance = &App{limiter: newIPLimiter()}
	apps.Register(instance)
}

// App is the device-pairing feature. It contributes no agent or MCP tools:
// pairing is a two-browser handshake, and nothing about it benefits from
// an agent in the middle.
type App struct {
	pool    *pgxpool.Pool
	signer  *auth.SessionSigner
	limiter *ipLimiter
}

// Init records the pool. Called by apps.Init.
func (a *App) Init(pool *pgxpool.Pool) { a.pool = pool }

// Configure wires the console session signer, which mints device sessions.
func Configure(signer *auth.SessionSigner) {
	if instance == nil {
		return
	}
	instance.signer = signer
}

func (a *App) Name() string { return AppName }

// SystemPrompt adds nothing: the agent has no part in pairing.
func (a *App) SystemPrompt() string { return "" }

func (a *App) ToolMetas() []services.ToolMeta { return nil }

func (a *App) RegisterAgentTools(_ context.Context, _ any, _ *services.Caller, _ bool) {}

func (a *App) RegisterMCPTools(_ *pgxpool.Pool, _ *services.Services) []mcpserver.ServerTool {
	return nil
}

// RegisterRoutes mounts the device-side pairing page and the admin API.
func (a *App) RegisterRoutes(mux apps.Mux) {
	if a.pool == nil || a.signer == nil {
		return
	}
	registerPairRoutes(mux, a)
	registerConsoleRoutes(mux, a)
}
