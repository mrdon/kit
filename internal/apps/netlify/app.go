// Package netlify is the Kit app that lets a non-technical board
// member request website changes from Slack and have them shipped via
// Netlify Agent Runners + GitHub. See docs/netlify-app.md for the full
// design spec.
//
// The app is the *non-technical surface*. A technical operator is
// expected to drive the same repo directly with `claude` / `codex`;
// nothing Kit does is required for the site to keep working.
package netlify

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/admin"
	"github.com/mrdon/kit/internal/apps/github"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/services"
)

var instance *App

func init() {
	instance = &App{}
	apps.Register(instance)
}

// App is the Netlify website-management app for Kit. v1 surfaces a
// PWA settings page where the tenant connects their Netlify account
// (OAuth) and installs the Kit GitHub App. Subsequent commits add the
// Slack agent surface for actual change requests.
type App struct {
	pool    *pgxpool.Pool
	svc     *Service
	signer  *auth.SessionSigner
	enc     *crypto.Encryptor
	baseURL string
}

// Init wires the service after the DB pool is available. Called by
// apps.Init from cmd/kit/main.go after migrations succeed.
func (a *App) Init(pool *pgxpool.Pool) {
	a.pool = pool
	// Encryptor is wired via Configure below; build the service then
	// and re-attach the pool.
}

// Configure wires the runtime surfaces:
//   - signer: PWA session middleware for /{slug}/apps/netlify/...
//   - enc: AES-256-GCM encryptor for token storage
//   - baseURL: external origin used to construct OAuth redirect URIs
//   - netlifyClientID/Secret: Netlify OAuth app credentials
//   - githubSvc: handle to the github Kit app's service (workspace-
//     shared install substrate)
//
// Any unset Netlify credential leaves the corresponding "Connect"
// button disabled on the settings page. GitHub readiness is queried
// from the injected github service so the netlify app doesn't carry
// duplicate config.
func Configure(
	signer *auth.SessionSigner,
	enc *crypto.Encryptor,
	baseURL string,
	netlifyClientID, netlifyClientSecret string,
	githubSvc *github.Service,
) {
	if instance == nil {
		return
	}
	instance.signer = signer
	instance.enc = enc
	instance.baseURL = baseURL
	if instance.pool != nil {
		instance.svc = NewService(instance.pool, enc)
		instance.svc.netlifyClientID = netlifyClientID
		instance.svc.netlifyClientSecret = netlifyClientSecret
		instance.svc.github = githubSvc
		instance.svc.baseURL = baseURL
	}
	admin.RegisterIntegration(&netlifyIntegration{app: instance})
}

// Service returns the live service, or nil before Configure has run.
func (a *App) Service() *Service { return a.svc }

func (a *App) Name() string { return "netlify" }

func (a *App) SystemPrompt() string { return "" }

func (a *App) ToolMetas() []services.ToolMeta { return nil }

func (a *App) RegisterAgentTools(_ context.Context, _ any, _ *services.Caller, _ bool) {
}

func (a *App) RegisterMCPTools(_ *pgxpool.Pool, _ *services.Services) []mcpserver.ServerTool {
	return nil
}

func (a *App) RegisterRoutes(mux *http.ServeMux) {
	if a.svc == nil || a.pool == nil || a.signer == nil {
		return
	}
	registerNetlifyRoutes(mux, a)
}

func (a *App) CronJobs() []apps.CronJob { return nil }
