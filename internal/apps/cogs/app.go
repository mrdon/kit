// Package cogs is production state for a brewery: what beer is in which
// vessel, what was packaged from it and what happened to it since. The
// brewer logs physical facts from a paired iPad; the console sets up the
// brewery, loads day one, plans brews and fixes mistakes. See
// docs/cogs-spec.md for the design and what later phases add (the
// planner, COGS, TTB).
package cogs

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/services"
)

// AppName is the registry identifier, used for enablement and URLs.
const AppName = "cogs"

var instance *App

func init() {
	instance = &App{}
	apps.Register(instance)
}

// App is the COGS feature app.
type App struct {
	pool   *pgxpool.Pool
	svc    *Service
	signer *auth.SessionSigner
}

// Init builds the service once the pool exists. Called by apps.Init.
func (a *App) Init(pool *pgxpool.Pool) {
	a.pool = pool
	a.svc = NewService(pool)
}

// Configure wires the console session signer.
func Configure(signer *auth.SessionSigner) {
	if instance == nil {
		return
	}
	instance.signer = signer
}

func (a *App) Name() string { return AppName }

// DisplayName and Description make the app toggleable on the Apps page.
func (a *App) DisplayName() string { return "COGS" }
func (a *App) Description() string {
	return "What's in every tank, what was packaged, and the brewer's log from a brewhouse iPad."
}

func (a *App) SystemPrompt() string { return systemPrompt }

func (a *App) ToolMetas() []services.ToolMeta { return toolMetas() }

func (a *App) RegisterAgentTools(_ context.Context, registerer any, caller *services.Caller, _ bool) {
	registerAgentTools(registerer, caller, a)
}

func (a *App) RegisterMCPTools(_ *pgxpool.Pool, _ *services.Services) []mcpserver.ServerTool {
	return registerMCPTools(a)
}

// RegisterRoutes mounts the brewer's API (capability-gated, so the paired
// iPad and any member share it) and the console's setup API (admin).
func (a *App) RegisterRoutes(mux apps.Mux) {
	if a.pool == nil || a.svc == nil || a.signer == nil {
		return
	}
	registerBrewerRoutes(mux, a)
	registerSetupRoutes(mux, a)
}

// Usage reports the vessel count for the Apps settings page.
func (a *App) Usage(ctx context.Context, tenantID uuid.UUID) (string, error) {
	if a.pool == nil {
		return "", nil
	}
	var n int
	if err := a.pool.QueryRow(ctx, `SELECT COUNT(*) FROM app_cogs_vessels
		WHERE tenant_id = $1 AND retired_at IS NULL`, tenantID).Scan(&n); err != nil {
		return "", err
	}
	return apps.CountLabel(n, "vessel", "vessels"), nil
}

const systemPrompt = `COGS (brewery production): get_cogs_state shows what is in every tank and barrel, packaged stock, ` +
	`this week's jobs and the brew plan; get_cogs_history lists what was logged. The brewer logs from ` +
	`the brewhouse iPad and corrections are made in the console, so point people there to change anything.`
