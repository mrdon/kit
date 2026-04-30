package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/stdlib"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/redis/go-redis/v9"

	"github.com/mrdon/kit/internal"
	"github.com/mrdon/kit/internal/anthropic"
	"github.com/mrdon/kit/internal/apps"
	builderapp "github.com/mrdon/kit/internal/apps/builder"
	_ "github.com/mrdon/kit/internal/apps/calendar"
	"github.com/mrdon/kit/internal/apps/cards"
	"github.com/mrdon/kit/internal/apps/coordination"
	"github.com/mrdon/kit/internal/apps/email"
	"github.com/mrdon/kit/internal/apps/integrations"
	_ "github.com/mrdon/kit/internal/apps/slack"
	"github.com/mrdon/kit/internal/apps/todo"
	"github.com/mrdon/kit/internal/apps/vault"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/buildinfo"
	"github.com/mrdon/kit/internal/config"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/database"
	"github.com/mrdon/kit/internal/logger"
	kitmcp "github.com/mrdon/kit/internal/mcp"
	"github.com/mrdon/kit/internal/scheduler"
	"github.com/mrdon/kit/internal/services"
	kitslack "github.com/mrdon/kit/internal/slack"
	"github.com/mrdon/kit/internal/tools"
	"github.com/mrdon/kit/internal/transcribe"
	"github.com/mrdon/kit/internal/web"
)

func main() {
	logger.Init()

	slog.Info("starting kit",
		"version", buildinfo.Version,
		"commit", buildinfo.Commit,
		"date", buildinfo.Date,
	)

	cfg, err := config.Load()
	if err != nil {
		slog.Error("loading config", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool, err := database.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("connecting to database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	// Run migrations using stdlib adapter
	sqlDB := stdlib.OpenDBFromPool(pool)
	if err := database.Migrate(sqlDB); err != nil {
		slog.Error("running migrations", "error", err)
		os.Exit(1)
	}
	sqlDB.Close()

	slog.Info("migrations complete")

	// Initialize apps (lets them set up services after DB is ready)
	apps.Init(pool)

	// Wire the exposed-tool registry source. The builder app owns the
	// exposed_tools table; tools.Registry asks it per session for the
	// caller-visible set. Done after apps.Init so the builder app's pool
	// is populated. Stays nil-safe in tests via tools.SetExposedToolRunner(nil).
	for _, a := range apps.All() {
		if b, ok := a.(*builderapp.App); ok {
			tools.SetExposedToolRunner(b.ExposedToolRunner())
			break
		}
	}

	// Encryption for bot tokens (needed by services.New below + later
	// internal.NewApp). Constructed before the builder runtime so the
	// shared services bundle is ready when we install script-run deps.
	enc, err := crypto.NewEncryptor(cfg.EncryptionKey)
	if err != nil {
		slog.Error("initializing encryptor", "error", err)
		os.Exit(1)
	}

	// Shared services bundle, reused by the MCP server below.
	svc := services.New(pool, enc)
	// Anthropic client for the builder LLM builtins. internal.NewApp
	// constructs its own client for the Slack agent; sharing isn't
	// worthwhile since the client is a stateless HTTP wrapper.
	builderLLM := anthropic.NewClient(cfg.AnthropicAPIKey)

	// Install the builder script runtime. Without this, every run_script
	// call (admin or via an exposed tool) errors with "engine not wired".
	closeBuilder, err := builderapp.InstallScriptRunDeps(pool, svc, enc, builderLLM)
	if err != nil {
		slog.Error("installing builder script runtime", "error", err)
		os.Exit(1)
	}
	defer func() {
		if cerr := closeBuilder(); cerr != nil {
			slog.Warn("closing builder runtime", "error", cerr)
		}
	}()

	// PWA session signer. Prefer an explicit KIT_SESSION_SECRET; fall back
	// to deriving from ENCRYPTION_KEY (domain-separated in NewSessionSigner)
	// so Dokku deploys don't need a second secret.
	sessionSecret := cfg.SessionSecret
	if sessionSecret == "" {
		sessionSecret = cfg.EncryptionKey
	}

	// Wire the integrations app's HTTP-level deps (encryptor, base URL,
	// signing secret). Done after sessionSecret resolves so we reuse the
	// same fallback chain.
	integrations.Configure(enc, cfg.BaseURL, sessionSecret)
	email.Configure(enc)
	// Wire the todo app's resolution-suggester deps: the LLM for the
	// Haiku suggester, the TaskService for spawning tasks when the user
	// taps a resolution chip, and the encryptor for decrypting the
	// tenant bot token at DM-open time.
	todo.Configure(builderLLM, svc.Tasks, enc)
	sessionSigner, err := auth.NewSessionSigner(sessionSecret)
	if err != nil {
		slog.Warn("session signer not configured — PWA endpoints disabled", "error", err)
		sessionSigner = nil
	} else {
		cards.Configure(
			sessionSigner,
			auth.SlackOpenIDConfig{ClientID: cfg.SlackClientID, ClientSecret: cfg.SlackClientSecret},
			cfg.BaseURL,
			cfg.Env == "dev",
		)
	}

	// Redis (optional — web_fetch caching)
	var rdb *redis.Client
	if cfg.RedisURL != "" {
		opts, err := redis.ParseURL(cfg.RedisURL)
		if err != nil {
			slog.Error("parsing redis URL", "error", err)
			os.Exit(1)
		}
		rdb = redis.NewClient(opts)
		if err := rdb.Ping(ctx).Err(); err != nil {
			slog.Warn("redis not available, web_fetch caching disabled", "error", err)
			rdb = nil
		} else {
			slog.Info("redis connected")
			defer rdb.Close()
		}
	}

	// Core application
	app := internal.NewApp(pool, enc, cfg.AnthropicAPIKey, cfg.BaseURL, rdb)

	// Optional voice transcription. If whisper env vars aren't set the
	// chat/transcribe endpoint returns a "not configured" error event;
	// typed chat still works without it.
	var transcriber transcribe.Transcriber
	if t, err := transcribe.New(cfg.WhisperBin, cfg.WhisperModel, cfg.FFmpegBin); err == nil {
		transcriber = t
	} else if !errors.Is(err, transcribe.ErrNotConfigured) {
		slog.Warn("whisper transcription disabled", "error", err)
	}
	cards.ConfigureChat(app.Agent, enc, transcriber)

	// Coordination needs builderLLM, the Messenger from app, the CardService
	// (for surfacing decision cards), and the TaskService (for shepherd
	// tasks). Wired here because Messenger lives on app.
	coordination.Configure(builderLLM, app.Messenger, cards.ServiceForGating(), svc.Tasks)

	// Vault uses the same CardService for admin grant-request decision
	// cards, plus the session signer for HTTP routes. Wrapped in a thin
	// adapter so the vault package doesn't import internal/apps/cards
	// directly (keeps the dep graph one-way).
	vault.Configure(newVaultCardAdapter(cards.ServiceForGating()), sessionSigner)

	// Task scheduler
	sched := scheduler.New(pool, enc, app.Agent)

	// Gated-tool wiring (§11 of decision-cards-as-gated-tool-calls plan).
	//
	// 1. PolicyLookup: given a tool name, return its DefaultPolicy.
	//    CreateDecision uses this to stamp is_gate_artifact; ResolveDecision
	//    re-checks it at approval time to refuse tamper.
	// 2. GateCreator: CardService is the sink for auto-gated tool calls
	//    intercepted by Registry.Execute.
	// 3. ToolExecutor: called from ResolveDecision after a user approves
	//    a gated option; builds a per-caller registry and dispatches the
	//    tool with approval.WithToken(ctx, ...) so Registry.Execute lets
	//    the handler run.
	//
	// A registry built for a throwaway caller is used for the policy
	// lookup — the Def.DefaultPolicy field is the same regardless of who
	// invokes the tool, so we only need to build one at startup to snapshot
	// policies. Dynamic (builder-exposed) tools don't set DefaultPolicy,
	// so they're always PolicyAllow and safe to miss here.
	staticPolicies := snapshotToolPolicies(ctx)
	cards.ConfigurePolicyLookup(func(name string) tools.Policy {
		return staticPolicies[name]
	})
	tools.SetGateCreator(cards.ServiceForGating())
	kitmcp.SetGateCreator(cards.ServiceForGating())
	cards.ConfigureToolExecutor(buildResolveToolExecutor(pool, svc, enc, app.Fetcher, builderLLM))

	// Stuck-resolving sweep: every scheduler tick (60s) any card stuck
	// in 'resolving' past its deadline gets flipped back to 'pending'.
	scheduler.RegisterPeriodicSweep(cards.PeriodicSweep())

	sched.Start(ctx)
	// Let ResolveDecision wake the scheduler immediately on resume so
	// workflows advance within a second of the user tapping, not up to
	// the next 60s poll.
	cards.ConfigureKicker(sched)

	// App-level periodic jobs (e.g. calendar sync)
	apps.RunCronJobs(ctx, pool, enc)

	// Slack event handler
	slackHandler := kitslack.NewHandler(cfg.SlackSigningSecret, app.HandleSlackEvent)

	// OAuth handler
	oauthHandler := kitslack.NewOAuthHandler(cfg.SlackClientID, cfg.SlackClientSecret, pool, enc, app.HandlePostInstall)

	// MCP server + OAuth (svc was constructed earlier for the builder
	// runtime; reuse it here so both surfaces share one services bundle).
	mcpHolder := kitmcp.NewServer(pool, svc, app.Agent, enc, sched)

	// Wire builder-published tools into MCP via per-session tool maps.
	// - InstallExposedToolRegistry gives the per-session register hook
	//   access to the MCPServer, pool, and the invoke dispatcher.
	// - SetExposedToolHooks tells the builder app_expose_tool /
	//   app_revoke_tool handlers to fan the change out to live sessions.
	kitmcp.InstallExposedToolRegistry(mcpHolder, func(ctx context.Context, caller *services.Caller, toolName string, args map[string]any) (string, error) {
		return builderapp.InvokeExposedTool(ctx, pool, caller, toolName, args)
	})
	builderapp.SetExposedToolHooks(kitmcp.PublishExposedTool, kitmcp.RevokeExposedTool)

	mcpHTTP := mcpserver.NewStreamableHTTPServer(mcpHolder.Server,
		mcpserver.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			return auth.InjectCallerFromRequest(ctx, pool, r)
		}),
	)
	oauthServer := auth.NewOAuthServer(pool, cfg.BaseURL, cfg.SlackClientID, cfg.SlackClientSecret, sessionSecret, sessionSigner)
	regHandler := auth.NewRegistrationHandler(pool)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.Handle("POST /slack/events", slackHandler)
	mux.HandleFunc("GET /slack/install", oauthHandler.HandleInstall)
	mux.HandleFunc("GET /slack/oauth/callback", oauthHandler.HandleCallback)

	// Per-tenant MCP + OAuth surface. Each Slack workspace is its own
	// authorization server at /{slug}/. Slack's redirect URI stays global
	// at /oauth/callback so the Slack app config never has to change.
	//
	// Discovery metadata (RFC 8414 / RFC 9728) lives at the ROOT under
	// /.well-known/... with the tenant slug appended as a path segment,
	// because RFCs 8414 and 9728 define the well-known prefix as always
	// starting at the origin. Clients including Claude Code's MCP SDK
	// only probe this form, not /{slug}/.well-known/... .
	//
	// OAuth endpoints (authorize/token/register) stay under /{slug}/ since
	// their URLs are advertised by the metadata documents. They're wrapped
	// in CORS middleware per the MCP auth spec — browser-based MCP
	// clients do preflight OPTIONS on DCR/token endpoints and fail if we
	// don't respond.
	tenantMW := auth.TenantFromPath(pool)
	metadataH := tenantMW(auth.CORS(http.HandlerFunc(oauthServer.HandleMetadata)))
	resourceH := tenantMW(auth.CORS(http.HandlerFunc(oauthServer.HandleResourceMetadata)))
	authorizeH := tenantMW(auth.CORS(http.HandlerFunc(oauthServer.HandleAuthorize)))
	tokenH := tenantMW(auth.CORS(http.HandlerFunc(oauthServer.HandleToken)))
	registerH := tenantMW(auth.CORS(http.HandlerFunc(regHandler.HandleRegister)))
	mux.Handle("GET /.well-known/oauth-authorization-server/{slug}", metadataH)
	mux.Handle("OPTIONS /.well-known/oauth-authorization-server/{slug}", metadataH)
	mux.Handle("GET /.well-known/oauth-protected-resource/{slug}/mcp", resourceH)
	mux.Handle("OPTIONS /.well-known/oauth-protected-resource/{slug}/mcp", resourceH)
	mux.Handle("GET /{slug}/oauth/authorize", authorizeH)
	mux.Handle("OPTIONS /{slug}/oauth/authorize", authorizeH)
	mux.Handle("POST /{slug}/oauth/token", tokenH)
	mux.Handle("OPTIONS /{slug}/oauth/token", tokenH)
	mux.Handle("POST /{slug}/oauth/register", registerH)
	mux.Handle("OPTIONS /{slug}/oauth/register", registerH)

	// Streamable HTTP uses POST for JSON-RPC, GET for the SSE stream, and
	// DELETE for session close. Each method is registered explicitly so Go's
	// ServeMux can resolve specificity vs the cards SPA's "GET /{slug}/".
	// MCPAuthGate enforces Bearer auth and returns 401 with a
	// resource_metadata pointer so clients can discover the auth server.
	mcpWrapped := tenantMW(auth.CORS(auth.MCPAuthGate(pool, cfg.BaseURL, mcpHTTP)))
	mux.Handle("POST /{slug}/mcp", mcpWrapped)
	mux.Handle("GET /{slug}/mcp", mcpWrapped)
	mux.Handle("DELETE /{slug}/mcp", mcpWrapped)
	mux.Handle("OPTIONS /{slug}/mcp", mcpWrapped)

	// Slack's OAuth callback stays global — the tenant slug rides inside
	// the signed state parameter.
	mux.HandleFunc("GET /oauth/callback", oauthServer.HandleCallback)

	// App routes
	apps.RegisterAllRoutes(mux)

	// Landing page
	mux.HandleFunc("GET /{$}", web.NewLandingHandler(cfg.BaseURL))

	server := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		slog.Info("listening", "port", cfg.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-shutdown
	slog.Info("shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(ctx, 30*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "error", err)
	}

	slog.Info("shutdown complete")
}
