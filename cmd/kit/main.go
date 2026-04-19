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
	"github.com/mrdon/kit/internal/apps"
	_ "github.com/mrdon/kit/internal/apps/calendar"
	"github.com/mrdon/kit/internal/apps/cards"
	_ "github.com/mrdon/kit/internal/apps/slack"
	_ "github.com/mrdon/kit/internal/apps/todo"
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

	// PWA session signer. Prefer an explicit KIT_SESSION_SECRET; fall back
	// to deriving from ENCRYPTION_KEY (domain-separated in NewSessionSigner)
	// so Dokku deploys don't need a second secret.
	sessionSecret := cfg.SessionSecret
	if sessionSecret == "" {
		sessionSecret = cfg.EncryptionKey
	}
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

	// Encryption for bot tokens
	enc, err := crypto.NewEncryptor(cfg.EncryptionKey)
	if err != nil {
		slog.Error("initializing encryptor", "error", err)
		os.Exit(1)
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

	// Task scheduler
	sched := scheduler.New(pool, enc, app.Agent)
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

	// MCP server + OAuth
	svc := services.New(pool, enc)
	mcpHolder := kitmcp.NewServer(pool, svc, app.Agent, enc, sched)
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
	tenantMW := auth.TenantFromPath(pool)
	mux.Handle("GET /{slug}/.well-known/oauth-authorization-server", tenantMW(http.HandlerFunc(oauthServer.HandleMetadata)))
	mux.Handle("GET /{slug}/oauth/authorize", tenantMW(http.HandlerFunc(oauthServer.HandleAuthorize)))
	mux.Handle("POST /{slug}/oauth/token", tenantMW(http.HandlerFunc(oauthServer.HandleToken)))
	mux.Handle("POST /{slug}/oauth/register", tenantMW(http.HandlerFunc(regHandler.HandleRegister)))
	mcpWrapped := tenantMW(auth.AssertBearerMatchesPathTenant(pool, mcpHTTP))
	// Streamable HTTP uses POST for JSON-RPC, GET for the SSE stream, and
	// DELETE for session close. Each method is registered explicitly so Go's
	// ServeMux can resolve specificity vs the cards SPA's "GET /{slug}/".
	mux.Handle("POST /{slug}/mcp", mcpWrapped)
	mux.Handle("GET /{slug}/mcp", mcpWrapped)
	mux.Handle("DELETE /{slug}/mcp", mcpWrapped)

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
