// Package github is the Kit app that owns the per-tenant Kit GitHub
// App installation. It's the GitHub analogue of Kit's single Slack
// bot: one Kit GitHub App registered with GitHub, one installation
// per tenant, used by every Kit feature that needs git/GitHub access.
//
// In v1 it has no UI of its own — the install button lives on the
// Netlify app's settings page (the only feature that uses GitHub
// today). When a second feature lands that needs GitHub, this app
// gains a workspace-level settings surface so the install isn't
// re-rendered per feature.
package github

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/admin"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/services"
)

var instance *App

func init() {
	instance = &App{}
	apps.Register(instance)
}

// App is the GitHub-App-substrate Kit app. Currently exposes only a
// service surface other apps consume via GetService(); no agent
// tools, MCP tools, or cron jobs in v1.
type App struct {
	pool    *pgxpool.Pool
	svc     *Service
	signer  *auth.SessionSigner
	baseURL string
}

// Init wires the service after the DB pool is available.
func (a *App) Init(pool *pgxpool.Pool) {
	a.pool = pool
	if a.svc == nil {
		a.svc = NewService(pool)
	} else {
		a.svc.pool = pool
	}
}

// Configure wires runtime surfaces:
//   - signer: PWA session middleware for the connect/disconnect routes
//   - baseURL: external origin used to construct the OAuth callback URL
//   - appSlug/appID/privateKey: Kit GitHub App credentials
//
// Empty credentials leave the install flow disabled; the netlify
// settings page renders a "GitHub not configured" hint.
func Configure(
	signer *auth.SessionSigner,
	baseURL, appSlug string,
	appID int64,
	privateKey []byte,
) {
	if instance == nil {
		return
	}
	instance.signer = signer
	instance.baseURL = baseURL
	if instance.svc == nil {
		instance.svc = &Service{}
	}
	instance.svc.appSlug = appSlug
	instance.svc.appID = appID
	instance.svc.privateKey = privateKey
	instance.svc.baseURL = baseURL

	admin.RegisterIntegration(&githubIntegration{app: instance})
}

// GetService returns the shared service. Other Kit apps (notably
// netlify) call this from their own Configure to depend on the
// GitHub install. Returns nil before Init has run — callers should
// nil-check and degrade gracefully (Kit is otherwise usable even if
// GitHub isn't configured at boot).
func GetService() *Service {
	if instance == nil {
		return nil
	}
	return instance.svc
}

func (a *App) Name() string { return "github" }

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
	registerGitHubRoutes(mux, a)
}

func (a *App) CronJobs() []apps.CronJob { return nil }
