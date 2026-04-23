// Package builder: meta_apps.go implements the app-CRUD meta-tools — the
// five tools admins use to create, list, inspect, and retire the "app
// bundle" that owns scripts, schedules, exposed tools, and item
// collections. Wiring lives in app.go's RegisterAgentTools / RegisterMCPTools.
//
// Contract summary (consumed by both the agent and MCP adapters):
//
//	create_app(name, description)      → { id, name, description, created_at }
//	list_apps()                        → [ app1, app2, ... ]
//	get_app(name)                      → app with scripts / schedules / exposed_tools
//	delete_app(name, confirm=true)     → { deleted: name }
//	purge_app_data(name, confirm=true) → { purged: N }
//
// Destructive tools go through requireConfirm. delete_app's FK on app_items
// is ON DELETE RESTRICT, so admins must purge first — we pre-check the
// count and return a readable error instead of surfacing the raw FK violation.
package builder

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
)

// metaAppTools enumerates the five app-CRUD meta-tools. The shape is shared
// between agent registration (where it drives tools.Def) and MCP registration
// (where it drives mcpserver.ServerTool via apps.MCPToolFromMeta).
//
// AdminOnly=true for all five. Non-admin agents never see them (skipped in
// registerMetaAgentTools) and non-admin MCP callers get ErrForbidden from
// guardAdmin inside the handler.
var metaAppTools = []services.ToolMeta{
	{
		Name:        "create_app",
		Description: "Create a new builder app (bundle for scripts, schedules, exposed tools, and collections). Name must be unique within the tenant.",
		Schema: services.PropsReq(map[string]any{
			"name":        services.Field("string", "Short identifier (snake_case recommended, e.g. 'crm', 'mug_club')"),
			"description": services.Field("string", "Human-readable description of what this app does"),
		}, "name"),
		AdminOnly: true,
	},
	{
		Name:        "list_apps",
		Description: "List all builder apps in this tenant, ordered by name. Returns a JSON array of {id, name, description, created_at}.",
		Schema:      services.Props(map[string]any{}),
		AdminOnly:   true,
	},
	{
		Name:        "get_app",
		Description: "Fetch one app by name, including its inventory of scripts, scheduled scripts, and exposed tools.",
		Schema: services.PropsReq(map[string]any{
			"name": services.Field("string", "App name to look up"),
		}, "name"),
		AdminOnly: true,
	},
	{
		Name:        "delete_app",
		Description: "Delete an app plus its scripts, schedules, and exposed tools. Fails if any app_items still exist — call purge_app_data first. Requires confirm=true.",
		Schema: services.PropsReq(map[string]any{
			"name":    services.Field("string", "App name to delete"),
			"confirm": map[string]any{"type": "boolean", "description": "Must be true — safety interlock."},
		}, "name", "confirm"),
		AdminOnly: true,
	},
	{
		Name:        "purge_app_data",
		Description: "Delete every app_items row for this app. History rows are written by the trigger so the purge is auditable. Requires confirm=true.",
		Schema: services.PropsReq(map[string]any{
			"name":    services.Field("string", "App name whose data to purge"),
			"confirm": map[string]any{"type": "boolean", "description": "Must be true — safety interlock."},
		}, "name", "confirm"),
		AdminOnly: true,
	},
}

// MetaAppTools returns the five app-CRUD meta-tools' metadata. Used by
// App.ToolMetas() so the services layer can surface them in schemas.
func MetaAppTools() []services.ToolMeta { return metaAppTools }

// appSummary is the JSON shape returned by create_app / list_apps. Kept
// separate from BuilderApp so internal fields (tenant_id, created_by) don't
// leak to the LLM — the tool surface is a contract and we want it narrow.
type appSummary struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   string    `json:"created_at"`
}

// appDetail extends appSummary with the inventory returned by get_app. Each
// sub-slice is always non-nil so the JSON serialises as `[]` rather than
// `null` when there are no scripts/schedules/tools — cleaner for the LLM.
type appDetail struct {
	appSummary
	Scripts      []scriptSummary   `json:"scripts"`
	Schedules    []scheduleSummary `json:"schedules"`
	ExposedTools []exposedSummary  `json:"exposed_tools"`
}

type scriptSummary struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
}

type scheduleSummary struct {
	ID         uuid.UUID `json:"id"`
	ScriptName string    `json:"script_name"`
	FnName     string    `json:"fn_name"`
	Cron       string    `json:"cron"`
}

type exposedSummary struct {
	ID       uuid.UUID `json:"id"`
	ToolName string    `json:"tool_name"`
	FnName   string    `json:"fn_name"`
}

// toSummary is the one spot we convert the DB row to the JSON shape so field
// formatting (RFC3339Nano, omitempty choices) is consistent across handlers.
func toSummary(a *BuilderApp) appSummary {
	return appSummary{
		ID:          a.ID,
		Name:        a.Name,
		Description: a.Description,
		CreatedAt:   a.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
	}
}

// createApp inserts a new builder_apps row. The DB's UNIQUE (tenant_id, name)
// constraint handles the race window; we catch the SQLSTATE and return a
// friendly duplicate-name error.
func createApp(
	ctx context.Context,
	pool *pgxpool.Pool,
	caller *services.Caller,
	name, description string,
) (*BuilderApp, error) {
	if err := guardAdmin(caller); err != nil {
		return nil, err
	}
	if name == "" {
		return nil, errors.New("name must be non-empty")
	}

	var descArg any
	if description == "" {
		descArg = nil
	} else {
		descArg = description
	}

	var app BuilderApp
	err := pool.QueryRow(ctx, `
		INSERT INTO builder_apps (tenant_id, name, description, created_by)
		VALUES ($1, $2, $3, $4)
		RETURNING id, tenant_id, name, COALESCE(description, ''), created_by, created_at
	`, caller.TenantID, name, descArg, caller.UserID).Scan(
		&app.ID, &app.TenantID, &app.Name, &app.Description, &app.CreatedBy, &app.CreatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("app %q already exists in this tenant", name)
		}
		return nil, fmt.Errorf("creating app: %w", err)
	}
	return &app, nil
}

// listApps returns all apps in the caller's tenant ordered by name. The LLM
// uses this to decide whether to create or reuse, so the order needs to be
// deterministic.
func listApps(ctx context.Context, pool *pgxpool.Pool, caller *services.Caller) ([]BuilderApp, error) {
	if err := guardAdmin(caller); err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx, `
		SELECT id, tenant_id, name, COALESCE(description, ''), created_by, created_at
		FROM builder_apps
		WHERE tenant_id = $1
		ORDER BY name
	`, caller.TenantID)
	if err != nil {
		return nil, fmt.Errorf("listing apps: %w", err)
	}
	defer rows.Close()

	out := make([]BuilderApp, 0)
	for rows.Next() {
		var a BuilderApp
		if err := rows.Scan(&a.ID, &a.TenantID, &a.Name, &a.Description, &a.CreatedBy, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning app row: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating apps: %w", err)
	}
	return out, nil
}

// getAppDetail fetches one app plus its inventory. Three small queries run
// sequentially on the same pool — we chose readability over one monster join
// because the sub-lists are independent and this keeps the row shapes sane.
func getAppDetail(
	ctx context.Context,
	pool *pgxpool.Pool,
	caller *services.Caller,
	name string,
) (*appDetail, error) {
	if err := guardAdmin(caller); err != nil {
		return nil, err
	}

	app, err := loadBuilderAppByName(ctx, pool, caller.TenantID, name)
	if err != nil {
		return nil, err
	}

	scripts, err := loadAppScripts(ctx, pool, caller.TenantID, app.ID)
	if err != nil {
		return nil, err
	}
	schedules, err := loadAppSchedules(ctx, pool, caller.TenantID, app.ID)
	if err != nil {
		return nil, err
	}
	tools, err := loadAppExposedTools(ctx, pool, caller.TenantID, app.ID)
	if err != nil {
		return nil, err
	}

	return &appDetail{
		appSummary:   toSummary(app),
		Scripts:      scripts,
		Schedules:    schedules,
		ExposedTools: tools,
	}, nil
}

// loadAppScripts reads the scripts list. Returns [] rather than nil.
func loadAppScripts(ctx context.Context, pool *pgxpool.Pool, tenantID, appID uuid.UUID) ([]scriptSummary, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, name, COALESCE(description, '')
		FROM scripts
		WHERE tenant_id = $1 AND builder_app_id = $2
		ORDER BY name
	`, tenantID, appID)
	if err != nil {
		return nil, fmt.Errorf("loading scripts: %w", err)
	}
	defer rows.Close()
	out := make([]scriptSummary, 0)
	for rows.Next() {
		var s scriptSummary
		if err := rows.Scan(&s.ID, &s.Name, &s.Description); err != nil {
			return nil, fmt.Errorf("scanning script: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// loadAppSchedules reads builder_script tasks for the given app,
// surfacing the script name instead of the raw config UUID (admins read
// schedules by script name, not UUID). Only returns active entries —
// get_app is a "what's live right now?" read-out; history lives in
// app_list_schedules which returns inactive too.
func loadAppSchedules(ctx context.Context, pool *pgxpool.Pool, tenantID, appID uuid.UUID) ([]scheduleSummary, error) {
	rows, err := pool.Query(ctx, `
		SELECT t.id, s.name, t.config->>'fn_name', t.cron_expr
		FROM tasks t
		JOIN scripts s ON s.id = (t.config->>'script_id')::uuid
		               AND s.tenant_id = t.tenant_id
		WHERE t.tenant_id = $1
		  AND t.task_type = $3
		  AND t.status = $4
		  AND s.builder_app_id = $2
		ORDER BY s.name, t.config->>'fn_name'
	`, tenantID, appID, models.TaskTypeBuilderScript, models.TaskStatusActive)
	if err != nil {
		return nil, fmt.Errorf("loading schedules: %w", err)
	}
	defer rows.Close()
	out := make([]scheduleSummary, 0)
	for rows.Next() {
		var s scheduleSummary
		if err := rows.Scan(&s.ID, &s.ScriptName, &s.FnName, &s.Cron); err != nil {
			return nil, fmt.Errorf("scanning schedule: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// loadAppExposedTools joins exposed_tools to scripts via script_id so the
// filter by builder_app_id is correct — exposed_tools itself is tenant-scoped,
// not app-scoped, so the join is load-bearing.
func loadAppExposedTools(ctx context.Context, pool *pgxpool.Pool, tenantID, appID uuid.UUID) ([]exposedSummary, error) {
	rows, err := pool.Query(ctx, `
		SELECT et.id, et.tool_name, et.fn_name
		FROM exposed_tools et
		JOIN scripts s ON s.id = et.script_id
		WHERE et.tenant_id = $1 AND s.builder_app_id = $2
		ORDER BY et.tool_name
	`, tenantID, appID)
	if err != nil {
		return nil, fmt.Errorf("loading exposed tools: %w", err)
	}
	defer rows.Close()
	out := make([]exposedSummary, 0)
	for rows.Next() {
		var e exposedSummary
		if err := rows.Scan(&e.ID, &e.ToolName, &e.FnName); err != nil {
			return nil, fmt.Errorf("scanning exposed tool: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// deleteApp drops the app row. Pre-flight check: fail if any app_items exist,
// because the FK is ON DELETE RESTRICT and admins need a friendly message
// ("app has N items; call purge_app_data first") instead of a raw FK error.
func deleteApp(
	ctx context.Context,
	pool *pgxpool.Pool,
	caller *services.Caller,
	name string,
) error {
	if err := guardAdmin(caller); err != nil {
		return err
	}

	app, err := loadBuilderAppByName(ctx, pool, caller.TenantID, name)
	if err != nil {
		return err
	}

	var itemCount int64
	err = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM app_items WHERE tenant_id = $1 AND builder_app_id = $2
	`, caller.TenantID, app.ID).Scan(&itemCount)
	if err != nil {
		return fmt.Errorf("counting items: %w", err)
	}
	if itemCount > 0 {
		return fmt.Errorf("app %q has %d items; call purge_app_data first", name, itemCount)
	}

	// Capture exposed-tool names before the CASCADE wipes them so the
	// MCP revoke hook can push list_changed to live sessions. Best-effort;
	// a DB error here shouldn't block the delete — callers will still
	// see the app gone, they'll just keep stale entries in their session
	// tool maps until they reconnect.
	toolNames, toolsErr := loadAppExposedTools(ctx, pool, caller.TenantID, app.ID)
	if toolsErr != nil {
		slog.Warn("delete_app: pre-collecting exposed tools", "app", name, "error", toolsErr)
	}

	ct, err := pool.Exec(ctx, `
		DELETE FROM builder_apps WHERE tenant_id = $1 AND id = $2
	`, caller.TenantID, app.ID)
	if err != nil {
		return fmt.Errorf("deleting app: %w", err)
	}
	if ct.RowsAffected() == 0 {
		// Race: someone deleted between load and delete. Not fatal — caller
		// asked us to delete it and it's gone. Report as success (idempotent).
		slog.Info("delete_app: app vanished between load and delete", "tenant", caller.TenantID, "app", name)
	}

	// Fire the revoke hook for every tool the CASCADE just dropped so
	// live MCP sessions see list_changed. Hook nil means no MCP wiring
	// (tests).
	if exposedRevokeHook != nil {
		for _, t := range toolNames {
			if hookErr := exposedRevokeHook(ctx, caller, t.ToolName); hookErr != nil {
				_ = hookErr
			}
		}
	}
	return nil
}

// purgeAppData drops every app_items row for this app. The AFTER DELETE
// trigger writes history rows for each deletion so the purge is auditable.
// Returns the count deleted.
func purgeAppData(
	ctx context.Context,
	pool *pgxpool.Pool,
	caller *services.Caller,
	name string,
) (int64, error) {
	if err := guardAdmin(caller); err != nil {
		return 0, err
	}

	app, err := loadBuilderAppByName(ctx, pool, caller.TenantID, name)
	if err != nil {
		return 0, err
	}

	ct, err := pool.Exec(ctx, `
		DELETE FROM app_items WHERE tenant_id = $1 AND builder_app_id = $2
	`, caller.TenantID, app.ID)
	if err != nil {
		return 0, fmt.Errorf("purging app data: %w", err)
	}
	return ct.RowsAffected(), nil
}

// isUniqueViolation matches pgx's representation of SQLSTATE 23505. We avoid
// importing pgconn directly by reading the wrapped error's Code via the
// PgError interface the pgx driver exposes.
func isUniqueViolation(err error) bool {
	type pgErr interface {
		SQLState() string
	}
	var pe pgErr
	if errors.As(err, &pe) {
		return pe.SQLState() == "23505"
	}
	return false
}
