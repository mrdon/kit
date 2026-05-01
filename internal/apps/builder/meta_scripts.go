// Package builder: meta_scripts.go implements the script-CRUD meta-tools
// (app_create_script, app_update_script, app_list_scripts,
// app_get_script, app_delete_script). The sibling meta_scripts_run.go
// holds app_run_script and meta_scripts_rollback.go holds
// app_rollback_script_run — same package, same handler pattern as
// meta_apps.go, split only to keep each file under the 500-LOC soft cap.
//
// The `_app_` prefix distinguishes these app-scoped script ops from
// future tenant-level shared-library scripts.
//
// Contract summary:
//
//	app_create_script(app, name, body, description?) -> { id, name, app, current_rev_id, created_at }
//	app_update_script(app, name, body)               -> { id, name, app, current_rev_id, created_at }
//	app_list_scripts(app)                            -> [ { id, name, description }, ... ]
//	app_get_script(app, name)                        -> { id, name, description, body, created_at }
//	app_delete_script(app, name, confirm)            -> { deleted: "<name>" }
//
// Every tool is admin-only: non-admins get ErrForbidden via guardAdmin.
// app_create_script and app_update_script wrap their two INSERTs in a
// transaction so we never end up with an orphan scripts row whose
// current_rev_id is NULL on first-create, or a new revision row
// unreferenced on update.
package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/services"
)

// metaScriptTools enumerates the four script-CRUD meta-tools plus
// app_run_script and app_rollback_script_run. Admin-only across the board;
// registered via App.ToolMetas so the services catalog picks them up.
var metaScriptTools = []services.ToolMeta{
	{
		Name:        "app_create_script",
		Description: "Create a new script in a builder app. Appends the first script_revisions row and sets scripts.current_rev_id. Script names are unique within an app.",
		Schema: services.PropsReq(map[string]any{
			"app":         services.Field("string", "Builder app name that owns this script"),
			"name":        services.Field("string", "Script identifier (snake_case recommended)"),
			"body":        services.Field("string", "Python source of the script"),
			"description": services.Field("string", "Human-readable description (optional)"),
		}, "app", "name", "body"),
		AdminOnly: true,
	},
	{
		Name:        "app_update_script",
		Description: "Append a new revision to an existing app script and point current_rev_id at it. The prior revisions stay in script_revisions for audit.",
		Schema: services.PropsReq(map[string]any{
			"app":  services.Field("string", "Builder app name"),
			"name": services.Field("string", "Script identifier"),
			"body": services.Field("string", "New Python source"),
		}, "app", "name", "body"),
		AdminOnly: true,
	},
	{
		Name:        "app_list_scripts",
		Description: "List scripts in a builder app, returning { id, name, description } for each.",
		Schema: services.PropsReq(map[string]any{
			"app": services.Field("string", "Builder app name"),
		}, "app"),
		AdminOnly: true,
	},
	{
		Name:        "app_get_script",
		Description: "Fetch an app script's current revision body plus its metadata.",
		Schema: services.PropsReq(map[string]any{
			"app":  services.Field("string", "Builder app name"),
			"name": services.Field("string", "Script identifier"),
		}, "app", "name"),
		AdminOnly: true,
	},
	{
		Name:        "app_delete_script",
		Description: "Delete a script from a builder app. Fails if the script has any active schedules or exposed tools — revoke those first. Requires confirm=true. Revision history remains for audit, but the script becomes unrunnable.",
		Schema: services.PropsReq(map[string]any{
			"app":     services.Field("string", "Builder app name"),
			"name":    services.Field("string", "Script identifier"),
			"confirm": map[string]any{"type": "boolean", "description": "Must be true — safety interlock."},
		}, "app", "name", "confirm"),
		AdminOnly: true,
	},
	{
		Name:        "app_run_script",
		Description: "Invoke a function on an app script. Opens a script_runs row for audit, enforces per-tenant limits, and returns the function's result plus run metadata.",
		Schema: services.PropsReq(map[string]any{
			"app":    services.Field("string", "Builder app name"),
			"script": services.Field("string", "Script identifier"),
			"fn":     services.Field("string", "Function to call inside the script"),
			"args":   map[string]any{"type": "object", "description": "Keyword arguments passed to fn"},
			"limits": map[string]any{"type": "object", "description": "Optional per-run limit overrides (advanced)"},
		}, "app", "script", "fn"),
		AdminOnly: true,
	},
	{
		Name:        "app_rollback_script_run",
		Description: "Roll back the mutations made by a completed app-script run using the temporal history. Requires confirm=true.",
		Schema: services.PropsReq(map[string]any{
			"run_id":  services.Field("string", "script_runs.id to roll back"),
			"confirm": map[string]any{"type": "boolean", "description": "Must be true — safety interlock."},
		}, "run_id", "confirm"),
		AdminOnly: true,
	},
}

// MetaScriptTools exposes the script meta-tools for App.ToolMetas so the
// combined list covers Phase 4a + 4b without each call site duplicating
// the stitching.
func MetaScriptTools() []services.ToolMeta { return metaScriptTools }

// scriptDTO is the JSON shape app_create_script / app_update_script / app_get_script
// return. Kept narrow on purpose — tenant_id and created_by are internal.
type scriptDTO struct {
	ID           uuid.UUID  `json:"id"`
	Name         string     `json:"name"`
	App          string     `json:"app"`
	Description  string     `json:"description,omitempty"`
	CurrentRevID *uuid.UUID `json:"current_rev_id,omitempty"`
	Body         string     `json:"body,omitempty"`
	CreatedAt    string     `json:"created_at"`
}

// metaScriptAgentHandler returns the script-meta handler for a given
// tool name. Nil for unknown names so the registration loop in app.go
// can short-circuit.
func metaScriptAgentHandler(name string) func(ec *execContextLike, input json.RawMessage) (string, error) {
	switch name {
	case "app_create_script":
		return handleCreateScript
	case "app_update_script":
		return handleUpdateScript
	case "app_list_scripts":
		return handleListScripts
	case "app_get_script":
		return handleGetScript
	case "app_delete_script":
		return handleDeleteScript
	case "app_run_script":
		return handleRunScript
	case "app_rollback_script_run":
		return handleRollbackScriptRun
	default:
		return nil
	}
}

func handleCreateScript(ec *execContextLike, input json.RawMessage) (string, error) {
	m, err := parseInput(input)
	if err != nil {
		return "", err
	}
	appName, err := argString(m, "app")
	if err != nil {
		return "", err
	}
	name, err := argString(m, "name")
	if err != nil {
		return "", err
	}
	body, err := argString(m, "body")
	if err != nil {
		return "", err
	}
	desc, _ := argOptionalString(m, "description")

	dto, err := createScript(ec.Ctx, ec.Pool, ec.Caller, appName, name, body, desc)
	if err != nil {
		return "", err
	}
	return formatToolResult(dto)
}

func handleUpdateScript(ec *execContextLike, input json.RawMessage) (string, error) {
	m, err := parseInput(input)
	if err != nil {
		return "", err
	}
	appName, err := argString(m, "app")
	if err != nil {
		return "", err
	}
	name, err := argString(m, "name")
	if err != nil {
		return "", err
	}
	body, err := argString(m, "body")
	if err != nil {
		return "", err
	}
	dto, err := updateScript(ec.Ctx, ec.Pool, ec.Caller, appName, name, body)
	if err != nil {
		return "", err
	}
	return formatToolResult(dto)
}

func handleListScripts(ec *execContextLike, input json.RawMessage) (string, error) {
	m, err := parseInput(input)
	if err != nil {
		return "", err
	}
	appName, err := argString(m, "app")
	if err != nil {
		return "", err
	}
	out, err := listScripts(ec.Ctx, ec.Pool, ec.Caller, appName)
	if err != nil {
		return "", err
	}
	return formatToolResult(out)
}

func handleGetScript(ec *execContextLike, input json.RawMessage) (string, error) {
	m, err := parseInput(input)
	if err != nil {
		return "", err
	}
	appName, err := argString(m, "app")
	if err != nil {
		return "", err
	}
	name, err := argString(m, "name")
	if err != nil {
		return "", err
	}
	dto, err := getScript(ec.Ctx, ec.Pool, ec.Caller, appName, name)
	if err != nil {
		return "", err
	}
	return formatToolResult(dto)
}

func handleDeleteScript(ec *execContextLike, input json.RawMessage) (string, error) {
	m, err := parseInput(input)
	if err != nil {
		return "", err
	}
	appName, err := argString(m, "app")
	if err != nil {
		return "", err
	}
	name, err := argString(m, "name")
	if err != nil {
		return "", err
	}
	confirm, _ := m["confirm"].(bool)
	if !confirm {
		return "", errors.New("app_delete_script requires confirm=true")
	}
	if err := deleteScript(ec.Ctx, ec.Pool, ec.Caller, appName, name); err != nil {
		return "", err
	}
	return formatToolResult(map[string]any{"deleted": name})
}

// deleteScript removes a script row (cascade drops revisions, runs, and
// any unrevoked exposed_tools — but we refuse upfront when exposed tools
// or active schedules exist so the admin revokes them explicitly.
// Otherwise a script deletion could silently unpublish tools that live
// users depend on.
func deleteScript(
	ctx context.Context,
	pool *pgxpool.Pool,
	caller *services.Caller,
	appName, name string,
) error {
	if err := guardAdmin(caller); err != nil {
		return err
	}
	app, err := loadBuilderAppByName(ctx, pool, caller.TenantID, appName)
	if err != nil {
		return err
	}

	var scriptID uuid.UUID
	err = pool.QueryRow(ctx, `
		SELECT id FROM scripts
		WHERE tenant_id = $1 AND builder_app_id = $2 AND name = $3
	`, caller.TenantID, app.ID, name).Scan(&scriptID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("script %q not found in app %q", name, appName)
		}
		return fmt.Errorf("loading script: %w", err)
	}

	var exposedCount int
	if err = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM exposed_tools
		WHERE tenant_id = $1 AND script_id = $2 AND is_stale = false
	`, caller.TenantID, scriptID).Scan(&exposedCount); err != nil {
		return fmt.Errorf("counting exposed tools: %w", err)
	}
	if exposedCount > 0 {
		return fmt.Errorf("script %q has %d exposed tool(s) — revoke them first with app_revoke_tool", name, exposedCount)
	}

	var scheduleCount int
	if err = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM tasks
		WHERE tenant_id = $1
		  AND job_type = 'builder_script'
		  AND status = 'active'
		  AND (config->>'script_id')::uuid = $2
	`, caller.TenantID, scriptID).Scan(&scheduleCount); err != nil {
		return fmt.Errorf("counting schedules: %w", err)
	}
	if scheduleCount > 0 {
		return fmt.Errorf("script %q has %d active schedule(s) — unschedule first with app_unschedule_script", name, scheduleCount)
	}

	if _, err = pool.Exec(ctx, `
		DELETE FROM scripts WHERE tenant_id = $1 AND id = $2
	`, caller.TenantID, scriptID); err != nil {
		return fmt.Errorf("deleting script: %w", err)
	}
	return nil
}

// createScript performs the two-statement insert inside one transaction:
// scripts row, then script_revisions row, then UPDATE scripts.current_rev_id.
// A failure anywhere rolls the transaction back, so we never leave an
// orphan row. The UNIQUE (tenant_id, builder_app_id, name) constraint
// surfaces as a friendly "already exists" error via isUniqueViolation.
func createScript(
	ctx context.Context,
	pool *pgxpool.Pool,
	caller *services.Caller,
	appName, name, body, description string,
) (*scriptDTO, error) {
	if err := guardAdmin(caller); err != nil {
		return nil, err
	}
	if name == "" {
		return nil, errors.New("name must be non-empty")
	}
	if body == "" {
		return nil, errors.New("body must be non-empty")
	}

	app, err := loadBuilderAppByName(ctx, pool, caller.TenantID, appName)
	if err != nil {
		return nil, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("starting tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var descArg any
	if description == "" {
		descArg = nil
	} else {
		descArg = description
	}

	var scriptID uuid.UUID
	var createdAt string
	err = tx.QueryRow(ctx, `
		INSERT INTO scripts (tenant_id, builder_app_id, name, description, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')
	`, caller.TenantID, app.ID, name, descArg, caller.UserID).Scan(&scriptID, &createdAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("script %q already exists in app %q", name, appName)
		}
		return nil, fmt.Errorf("inserting script: %w", err)
	}

	var revID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO script_revisions (script_id, body, created_by)
		VALUES ($1, $2, $3)
		RETURNING id
	`, scriptID, body, caller.UserID).Scan(&revID)
	if err != nil {
		return nil, fmt.Errorf("inserting revision: %w", err)
	}

	if _, err = tx.Exec(ctx, `
		UPDATE scripts SET current_rev_id = $1
		WHERE tenant_id = $2 AND id = $3
	`, revID, caller.TenantID, scriptID); err != nil {
		return nil, fmt.Errorf("pointing current_rev_id: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing: %w", err)
	}

	revIDCopy := revID
	return &scriptDTO{
		ID:           scriptID,
		Name:         name,
		App:          appName,
		Description:  description,
		CurrentRevID: &revIDCopy,
		CreatedAt:    createdAt,
	}, nil
}

// updateScript appends a new revision and repoints scripts.current_rev_id.
// No rows are modified if the script does not exist — we explicitly
// pre-check so the error is "script not found" rather than a silent
// no-op or a FK-violation message.
func updateScript(
	ctx context.Context,
	pool *pgxpool.Pool,
	caller *services.Caller,
	appName, name, body string,
) (*scriptDTO, error) {
	if err := guardAdmin(caller); err != nil {
		return nil, err
	}
	if body == "" {
		return nil, errors.New("body must be non-empty")
	}

	app, err := loadBuilderAppByName(ctx, pool, caller.TenantID, appName)
	if err != nil {
		return nil, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("starting tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var scriptID uuid.UUID
	var description *string
	var createdAt string
	err = tx.QueryRow(ctx, `
		SELECT id, description,
		       to_char(created_at, 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')
		FROM scripts
		WHERE tenant_id = $1 AND builder_app_id = $2 AND name = $3
	`, caller.TenantID, app.ID, name).Scan(&scriptID, &description, &createdAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("script %q not found in app %q", name, appName)
		}
		return nil, fmt.Errorf("loading script: %w", err)
	}

	var revID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO script_revisions (script_id, body, created_by)
		VALUES ($1, $2, $3)
		RETURNING id
	`, scriptID, body, caller.UserID).Scan(&revID)
	if err != nil {
		return nil, fmt.Errorf("inserting revision: %w", err)
	}

	if _, err = tx.Exec(ctx, `
		UPDATE scripts SET current_rev_id = $1
		WHERE tenant_id = $2 AND id = $3
	`, revID, caller.TenantID, scriptID); err != nil {
		return nil, fmt.Errorf("updating current_rev_id: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing: %w", err)
	}

	desc := ""
	if description != nil {
		desc = *description
	}
	revIDCopy := revID
	return &scriptDTO{
		ID:           scriptID,
		Name:         name,
		App:          appName,
		Description:  desc,
		CurrentRevID: &revIDCopy,
		CreatedAt:    createdAt,
	}, nil
}

// listScripts returns the script summaries for one app. Mirrors the
// scriptSummary shape from meta_apps.go so the LLM sees a consistent
// result whether it called app_list_scripts or get_app.
func listScripts(
	ctx context.Context,
	pool *pgxpool.Pool,
	caller *services.Caller,
	appName string,
) ([]scriptSummary, error) {
	if err := guardAdmin(caller); err != nil {
		return nil, err
	}
	app, err := loadBuilderAppByName(ctx, pool, caller.TenantID, appName)
	if err != nil {
		return nil, err
	}
	return loadAppScripts(ctx, pool, caller.TenantID, app.ID)
}

// getScript fetches a script plus the body of its current revision. If
// current_rev_id is NULL (shouldn't happen after app_create_script's
// transaction, but defensively handled) we surface an error rather than
// returning an empty body that looks valid.
func getScript(
	ctx context.Context,
	pool *pgxpool.Pool,
	caller *services.Caller,
	appName, name string,
) (*scriptDTO, error) {
	if err := guardAdmin(caller); err != nil {
		return nil, err
	}
	app, err := loadBuilderAppByName(ctx, pool, caller.TenantID, appName)
	if err != nil {
		return nil, err
	}

	var (
		scriptID     uuid.UUID
		description  *string
		currentRevID *uuid.UUID
		body         *string
		createdAt    string
	)
	err = pool.QueryRow(ctx, `
		SELECT s.id, s.description, s.current_rev_id, sr.body,
		       to_char(s.created_at, 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')
		FROM scripts s
		LEFT JOIN script_revisions sr ON sr.id = s.current_rev_id
		WHERE s.tenant_id = $1 AND s.builder_app_id = $2 AND s.name = $3
	`, caller.TenantID, app.ID, name).Scan(&scriptID, &description, &currentRevID, &body, &createdAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("script %q not found in app %q", name, appName)
		}
		return nil, fmt.Errorf("loading script: %w", err)
	}
	if currentRevID == nil || body == nil {
		return nil, fmt.Errorf("script %q has no current revision", name)
	}
	desc := ""
	if description != nil {
		desc = *description
	}
	return &scriptDTO{
		ID:           scriptID,
		Name:         name,
		App:          appName,
		Description:  desc,
		CurrentRevID: currentRevID,
		Body:         *body,
		CreatedAt:    createdAt,
	}, nil
}
