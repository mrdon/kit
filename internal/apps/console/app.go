// Package console hosts the direct-manipulation desktop web UI served at
// /{slug}/web/*. It serves the React console shell (built in web/console)
// plus the JSON endpoints the shell needs that aren't owned by a feature
// app — today the launcher's /me probe and the integrations index.
//
// Each converged feature surface keeps its own JSON endpoints under
// /{slug}/web/api/... in its own app package; console owns only the shell
// and cross-app launcher concerns.
package console

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/services"
)

// Segment is the URL path segment the console mounts under: /{slug}/web.
// It is provisional — renaming touches this constant, the Vite base in
// web/console/vite.config.ts, the asset-prefix handler, and the client
// CONSOLE_SEGMENT in web/console/src/workspace.ts. Keep those few and
// centralized.
const Segment = "web"

var instance *App

func init() {
	instance = &App{}
	apps.Register(instance)
}

// App owns the console shell + cross-app launcher JSON.
type App struct {
	pool   *pgxpool.Pool
	signer *auth.SessionSigner
	enc    *crypto.Encryptor
}

// Init wires the pool. Called after migrations succeed.
func (a *App) Init(pool *pgxpool.Pool) {
	a.pool = pool
}

// Configure wires the runtime surfaces. enc is used by the roles page to
// list the full Slack workspace (it decrypts the tenant bot token).
func Configure(signer *auth.SessionSigner, enc *crypto.Encryptor) {
	if instance == nil {
		return
	}
	instance.signer = signer
	instance.enc = enc
}

func (a *App) Name() string { return apps.AppConsole }

func (a *App) SystemPrompt() string { return "" }

func (a *App) ToolMetas() []services.ToolMeta { return nil }

func (a *App) RegisterAgentTools(_ context.Context, _ any, _ *services.Caller, _ bool) {}

func (a *App) RegisterMCPTools(_ *pgxpool.Pool, _ *services.Services) []mcpserver.ServerTool {
	return nil
}

func (a *App) RegisterRoutes(mux apps.Mux) {
	if a.pool == nil || a.signer == nil {
		return
	}
	registerConsoleRoutes(mux, a)
}

func (a *App) CronJobs() []apps.CronJob { return nil }
