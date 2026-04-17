package main

import (
	"context"
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
	app := internal.NewApp(pool, enc, cfg.AnthropicAPIKey, rdb)

	// Task scheduler
	sched := scheduler.New(pool, enc, app.Agent)
	sched.Start(ctx)

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
	oauthServer := auth.NewOAuthServer(pool, cfg.BaseURL, cfg.SlackClientID, cfg.SlackClientSecret)
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

	// MCP endpoint (streamable HTTP)
	mux.Handle("/mcp", mcpHTTP)

	// OAuth endpoints for MCP authentication
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", oauthServer.HandleMetadata)
	mux.HandleFunc("GET /oauth/authorize", oauthServer.HandleAuthorize)
	mux.HandleFunc("POST /oauth/token", oauthServer.HandleToken)
	mux.HandleFunc("GET /oauth/callback", oauthServer.HandleCallback)
	mux.HandleFunc("POST /oauth/register", regHandler.HandleRegister)

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
