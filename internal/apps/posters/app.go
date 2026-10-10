// Package posters makes on-brand event posters from a tenant's own photo
// library. Press Generate on an event, see seven options in seconds, pick
// one, chat to change it, set it on the event.
//
// Two rules carry through every path and have no exceptions: no generated
// imagery (every image is a real photo from the library or an approved
// logo, and the renderer refuses drawing elements), and no invented facts
// (copy comes from the event's public fields, never prep_notes).
//
// Kit's own model use is small: a handful of Sonnet calls a person
// triggered (copy, the photo pick, chat edits, a facts refresh, and brand
// extraction when a guide has no token block). Photo indexing and template
// authoring run on a harness over MCP, on the caller's model.
package posters

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/redis/go-redis/v9"

	"github.com/mrdon/kit/internal/agent"
	"github.com/mrdon/kit/internal/anthropic"
	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/events"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/chat"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/posterrender"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

// AppName is the registry name and enablement key.
const AppName = "posters"

var instance *App

func init() {
	instance = &App{chatLimiter: chat.NewExecuteLimiter()}
	apps.Register(instance)
}

// App is the posters feature.
type App struct {
	pool     *pgxpool.Pool
	svc      *services.Services
	signer   *auth.SessionSigner
	enc      *crypto.Encryptor
	llm      anthropic.Sender
	agent    *agent.Agent
	renderer posterrender.Renderer
	stop     func()
	cache    *renderCache
	pixabay  *pixabayClient
	driveKey string
	baseURL  string
	// eventsSvc overrides how the events service is found; tests set it.
	eventsSvc func() *events.Service
	// chatLimiter is the same per-user window and in-flight cap card chat
	// applies; poster chat is not a way around it.
	chatLimiter *chat.ExecuteLimiter
}

// Instance exposes the registered app.
func Instance() *App { return instance }

// Init caches the pool, installs the built-in templates and declares the
// scheduled work. Called by apps.Init() before Configure.
func (a *App) Init(pool *pgxpool.Pool) {
	a.pool = pool
	a.cache = newRenderCache(nil)
	a.installBuiltins(context.Background())
	a.installEventListener()
	a.registerScheduledTasks()
}

// Config is what Configure needs beyond the shared services.
type Config struct {
	Renderer   posterrender.Config
	DriveKey   string
	PixabayKey string
	BaseURL    string
}

// Configure wires the services, the model, the renderer and the caches.
// The renderer is started here (a supervised child unless a URL names one
// elsewhere); a renderer that cannot start disables generation with a
// logged reason rather than stopping Kit.
func Configure(ctx context.Context, svc *services.Services, enc *crypto.Encryptor, signer *auth.SessionSigner, llm *anthropic.Client, ag *agent.Agent, rdb *redis.Client, cfg Config) {
	if instance == nil {
		return
	}
	a := instance
	a.svc, a.enc, a.signer, a.llm, a.agent = svc, enc, signer, llm, ag
	a.cache = newRenderCache(rdb)
	a.pixabay = newPixabay(cfg.PixabayKey, rdb)
	a.driveKey = cfg.DriveKey
	a.baseURL = cfg.BaseURL
	cfg.Renderer.DriveAPIKey = cfg.DriveKey
	if cfg.Renderer.URL == "" && cfg.Renderer.Dir == "" {
		slog.Warn("poster renderer not configured; poster generation is off (set POSTER_RENDERER_DIR or POSTER_RENDERER_URL)")
		return
	}
	r, stop, err := posterrender.Start(ctx, cfg.Renderer)
	if err != nil {
		slog.Error("poster renderer failed to start; poster generation is off", "error", err)
		return
	}
	a.renderer, a.stop = r, stop
}

// Shutdown stops the renderer child.
func (a *App) Shutdown() {
	if a.stop != nil {
		a.stop()
	}
}

// RendererReady reports whether renders can happen right now.
func (a *App) RendererReady(ctx context.Context) bool {
	return a.renderer != nil && a.renderer.Healthy(ctx)
}

func (a *App) Name() string { return AppName }

// DisplayName and Description make this a toggleable feature app.
func (a *App) DisplayName() string { return "Posters" }
func (a *App) Description() string {
	return "Generate on-brand event posters from your own photos, pick one, and edit it in chat."
}

// Usage is the one-line summary on the Apps settings page.
func (a *App) Usage(ctx context.Context, tenantID uuid.UUID) (string, error) {
	var n int
	if err := a.pool.QueryRow(ctx, `SELECT count(*) FROM app_posters WHERE tenant_id = $1 AND set_version_id IS NOT NULL`, tenantID).Scan(&n); err != nil {
		return "", fmt.Errorf("counting posters: %w", err)
	}
	return apps.CountLabel(n, "poster on an event", "posters on events"), nil
}

func (a *App) SystemPrompt() string { return mustRender("system_prompt.tmpl", nil) }

// ToolMetas is every tool either surface exposes, so the MCP visibility
// layer sees the admin-only index tools too.
func (a *App) ToolMetas() []services.ToolMeta { return allTools() }

func (a *App) RegisterAgentTools(ctx context.Context, registerer any, caller *services.Caller, isAdmin bool) {
	r, ok := registerer.(*tools.Registry)
	if !ok {
		return
	}
	a.registerAgentTools(ctx, r, caller, isAdmin)
}

func (a *App) RegisterMCPTools(_ *pgxpool.Pool, _ *services.Services) []mcpserver.ServerTool {
	return a.buildMCPTools()
}

// RegisterRoutes mounts the console JSON API and the poster chat endpoint.
func (a *App) RegisterRoutes(mux apps.Mux) {
	if a.pool == nil || a.signer == nil {
		return
	}
	registerConsoleRoutes(mux, a)
	registerChatRoutes(mux, a)
}
