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

	"github.com/mrdon/kit/internal"
	"github.com/mrdon/kit/internal/buildinfo"
	"github.com/mrdon/kit/internal/config"
	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/database"
	"github.com/mrdon/kit/internal/logger"
	kitslack "github.com/mrdon/kit/internal/slack"
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

	// Encryption for bot tokens
	enc, err := crypto.NewEncryptor(cfg.EncryptionKey)
	if err != nil {
		slog.Error("initializing encryptor", "error", err)
		os.Exit(1)
	}

	// Core application
	app := internal.NewApp(pool, enc, cfg.AnthropicAPIKey)

	// Slack event handler
	slackHandler := kitslack.NewHandler(cfg.SlackSigningSecret, app.HandleSlackEvent)

	// OAuth handler
	oauthHandler := kitslack.NewOAuthHandler(cfg.SlackClientID, cfg.SlackClientSecret, pool, enc, app.HandlePostInstall)

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
