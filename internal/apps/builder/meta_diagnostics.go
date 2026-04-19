// Package builder: meta_diagnostics.go implements the two Phase 4e
// diagnostic meta-tools — script_logs and script_stats. Both are admin-only
// and tenant-scoped; they exist so an admin debugging a misbehaving script
// can inspect its log() output and see aggregate health numbers without
// shelling into the DB.
//
// Contract summary:
//
//	script_logs(run_id, limit=100)
//	    -> [ {level, message, fields, created_at}, ... ]
//
//	script_stats(app=None, script=None, days=7)
//	    -> {completed, errors, limits, cancelled,
//	        avg_duration_ms, max_duration_ms,
//	        tokens, cost_cents, days, scope}
//
// Why these two and not more:
//   - `script_logs` maps 1:1 to the row emission from util_builtins.go's
//     dispatchLog — the admin-side read path needs to match the runtime
//     write path or the feature is useless.
//   - `script_stats` rolls up script_runs + llm_call_log into one shot so
//     admins don't have to join the two tables themselves. The 7-day
//     default window matches the landing-page "last week" convention.
//
// Tenant isolation is enforced in two places per tool:
//  1. `guardAdmin` rejects non-admin callers (mirrors every other meta-tool).
//  2. Every SQL clause filters on caller.TenantID. For script_logs the
//     handler pre-verifies run ownership via script_runs so a cross-tenant
//     run_id comes back as a clean "run not found" instead of leaking
//     other tenants' log rows via a silent empty result.
package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/services"
)

// Limits that cap user-supplied knobs so a hallucinated `limit=999999` or
// `days=3650` can't drag a tenant's Postgres to its knees.
const (
	scriptLogsDefaultLimit = 100
	scriptLogsMaxLimit     = 500
	scriptStatsDefaultDays = 7
	scriptStatsMaxDays     = 90
)

// metaDiagnosticTools enumerates the two diagnostic meta-tools. Admin-only
// across the board. The schemas mirror the agent/MCP contract shape used
// elsewhere in the package.
var metaDiagnosticTools = []services.ToolMeta{
	{
		Name:        "script_logs",
		Description: "Fetch per-run log lines written by a script via the log() built-in. Returns level, message, fields, and timestamp for each row.",
		Schema: services.PropsReq(map[string]any{
			"run_id": services.Field("string", "script_runs.id to fetch logs for"),
			"limit":  services.Field("integer", "Max rows to return (default 100, cap 500)"),
		}, "run_id"),
		AdminOnly: true,
	},
	{
		Name:        "script_stats",
		Description: "Aggregate script_runs + llm_call_log counters across a time window. Optional app/script filters narrow the scope.",
		Schema: services.Props(map[string]any{
			"app":    services.Field("string", "Builder app name to filter by (optional)"),
			"script": services.Field("string", "Script identifier to filter by (requires app)"),
			"days":   services.Field("integer", "Lookback window in days (default 7, cap 90)"),
		}),
		AdminOnly: true,
	},
}

// MetaDiagnosticTools exposes the diagnostic meta-tools so App.ToolMetas
// can include them in the combined catalogue.
func MetaDiagnosticTools() []services.ToolMeta { return metaDiagnosticTools }

// metaDiagnosticAgentHandler returns the handler for a given tool name.
// Nil for unknown names so the registration loop in app.go short-circuits.
func metaDiagnosticAgentHandler(name string) func(ec *execContextLike, input json.RawMessage) (string, error) {
	switch name {
	case "script_logs":
		return handleScriptLogs
	case "script_stats":
		return handleScriptStats
	default:
		return nil
	}
}

// scriptLogRow is the JSON shape script_logs returns for each row. We
// decode `fields` to a typed map so the LLM sees structured JSON rather
// than a re-encoded blob in a string.
type scriptLogRow struct {
	Level     string         `json:"level"`
	Message   string         `json:"message"`
	Fields    map[string]any `json:"fields,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

// scriptStatsResponse is the JSON shape script_stats returns. Separate
// counters per terminal status so admins can tell "limit_exceeded" apart
// from "error" without a follow-up query. `scope` is a human-readable
// descriptor of the applied filter so the LLM can echo it back when
// summarising the result.
type scriptStatsResponse struct {
	Completed     int    `json:"completed"`
	Errors        int    `json:"errors"`
	Limits        int    `json:"limits"`
	Cancelled     int    `json:"cancelled"`
	AvgDurationMs int    `json:"avg_duration_ms"`
	MaxDurationMs int    `json:"max_duration_ms"`
	Tokens        int    `json:"tokens"`
	CostCents     int    `json:"cost_cents"`
	Days          int    `json:"days"`
	Scope         string `json:"scope"`
}

func handleScriptLogs(ec *execContextLike, input json.RawMessage) (string, error) {
	m, err := parseInput(input)
	if err != nil {
		return "", err
	}
	runIDStr, err := argString(m, "run_id")
	if err != nil {
		return "", err
	}
	runID, err := uuid.Parse(runIDStr)
	if err != nil {
		return "", fmt.Errorf("run_id must be a UUID: %w", err)
	}
	limit, err := argOptionalInt(m, "limit")
	if err != nil {
		return "", err
	}
	if limit <= 0 {
		limit = scriptLogsDefaultLimit
	}
	if limit > scriptLogsMaxLimit {
		limit = scriptLogsMaxLimit
	}

	rows, err := fetchScriptLogs(ec.Ctx, ec.Pool, ec.Caller, runID, limit)
	if err != nil {
		return "", err
	}
	return formatToolResult(rows)
}

// fetchScriptLogs loads log rows for a given run. Tenant isolation is a
// two-step dance: verify the run exists and belongs to the caller's tenant
// first (clean "run not found" otherwise), then query script_logs
// restricted to that tenant. The pre-verification step is load-bearing —
// without it a cross-tenant run_id would silently return [] which looks
// indistinguishable from "this run had no logs".
func fetchScriptLogs(
	ctx context.Context,
	pool *pgxpool.Pool,
	caller *services.Caller,
	runID uuid.UUID,
	limit int,
) ([]scriptLogRow, error) {
	if err := guardAdmin(caller); err != nil {
		return nil, err
	}
	var exists bool
	err := pool.QueryRow(ctx, `
		SELECT TRUE FROM script_runs WHERE tenant_id = $1 AND id = $2
	`, caller.TenantID, runID).Scan(&exists)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("script_run %s not found", runID)
		}
		return nil, fmt.Errorf("loading script_run: %w", err)
	}

	rows, err := pool.Query(ctx, `
		SELECT level, message, fields, created_at
		FROM script_logs
		WHERE tenant_id = $1 AND script_run_id = $2
		ORDER BY id
		LIMIT $3
	`, caller.TenantID, runID, limit)
	if err != nil {
		return nil, fmt.Errorf("querying script_logs: %w", err)
	}
	defer rows.Close()

	out := make([]scriptLogRow, 0)
	for rows.Next() {
		var (
			level, message string
			fieldsJSON     []byte
			createdAt      time.Time
		)
		if err := rows.Scan(&level, &message, &fieldsJSON, &createdAt); err != nil {
			return nil, fmt.Errorf("scanning script_logs row: %w", err)
		}
		row := scriptLogRow{Level: level, Message: message, CreatedAt: createdAt}
		if len(fieldsJSON) > 0 {
			if err := json.Unmarshal(fieldsJSON, &row.Fields); err != nil {
				// Corrupt fields shouldn't kill the whole query — surface
				// the rest of the rows and drop fields for the broken one.
				row.Fields = nil
			}
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating script_logs rows: %w", err)
	}
	return out, nil
}

func handleScriptStats(ec *execContextLike, input json.RawMessage) (string, error) {
	m, err := parseInput(input)
	if err != nil {
		return "", err
	}
	appName, err := argOptionalString(m, "app")
	if err != nil {
		return "", err
	}
	scriptName, err := argOptionalString(m, "script")
	if err != nil {
		return "", err
	}
	if scriptName != "" && appName == "" {
		return "", errors.New("script filter requires an app filter")
	}
	days, err := argOptionalInt(m, "days")
	if err != nil {
		return "", err
	}
	if days <= 0 {
		days = scriptStatsDefaultDays
	}
	if days > scriptStatsMaxDays {
		days = scriptStatsMaxDays
	}

	stats, err := computeScriptStats(ec.Ctx, ec.Pool, ec.Caller, appName, scriptName, days)
	if err != nil {
		return "", err
	}
	return formatToolResult(stats)
}

// computeScriptStats runs the aggregate query across script_runs +
// llm_call_log. App/script filters resolve to UUIDs up front so the main
// query can bind IDs (parameterised SQL; no user-supplied string hits the
// query text). Missing filter subjects surface as a clean "app/script not
// found" error rather than returning zeroed-out aggregates that an admin
// might misread as "the script hasn't run" when the truth is "the filter
// didn't match anything".
func computeScriptStats(
	ctx context.Context,
	pool *pgxpool.Pool,
	caller *services.Caller,
	appName, scriptName string,
	days int,
) (*scriptStatsResponse, error) {
	if err := guardAdmin(caller); err != nil {
		return nil, err
	}

	scope := "all apps"
	var appID, scriptID *uuid.UUID
	if appName != "" {
		app, err := loadBuilderAppByName(ctx, pool, caller.TenantID, appName)
		if err != nil {
			return nil, err
		}
		appID = &app.ID
		scope = "app=" + appName
		if scriptName != "" {
			sc, err := loadScriptByName(ctx, pool, caller.TenantID, app.ID, scriptName)
			if err != nil {
				return nil, err
			}
			scriptID = &sc.ID
			scope = fmt.Sprintf("app=%s/script=%s", appName, scriptName)
		}
	}

	resp := &scriptStatsResponse{Days: days, Scope: scope}

	// Run the aggregate query against script_runs + llm_call_log with the
	// same filter applied to both subtrees. Nullable app_id/script_id
	// params let us express "no filter" without stringing together SQL.
	const q = `
		WITH runs AS (
		    SELECT sr.status, sr.duration_ms
		    FROM script_runs sr
		    JOIN scripts s ON s.id = sr.script_id
		    WHERE sr.tenant_id = $1
		      AND sr.started_at >= now() - ($2 || ' days')::interval
		      AND ($3::uuid IS NULL OR s.builder_app_id = $3)
		      AND ($4::uuid IS NULL OR sr.script_id = $4)
		),
		llm AS (
		    SELECT COALESCE(l.tokens_in, 0) + COALESCE(l.tokens_out, 0) AS tokens,
		           COALESCE(l.cost_cents, 0) AS cost_cents
		    FROM llm_call_log l
		    JOIN script_runs sr ON sr.id = l.script_run_id
		    JOIN scripts s ON s.id = sr.script_id
		    WHERE l.tenant_id = $1
		      AND sr.started_at >= now() - ($2 || ' days')::interval
		      AND ($3::uuid IS NULL OR s.builder_app_id = $3)
		      AND ($4::uuid IS NULL OR sr.script_id = $4)
		)
		SELECT
		    (SELECT COUNT(*) FROM runs WHERE status = 'completed')::int,
		    (SELECT COUNT(*) FROM runs WHERE status = 'error')::int,
		    (SELECT COUNT(*) FROM runs WHERE status = 'limit_exceeded')::int,
		    (SELECT COUNT(*) FROM runs WHERE status = 'cancelled')::int,
		    (SELECT COALESCE(AVG(duration_ms), 0) FROM runs)::int,
		    (SELECT COALESCE(MAX(duration_ms), 0) FROM runs)::int,
		    (SELECT COALESCE(SUM(tokens), 0) FROM llm)::int,
		    (SELECT COALESCE(SUM(cost_cents), 0) FROM llm)::int
	`
	// days arrives as a Go int; pgx pairs it with the `$2 || ' days'`
	// expression expecting text, so pass it stringified to avoid an
	// encoding-plan ambiguity.
	daysStr := strconv.Itoa(days)
	err := pool.QueryRow(ctx, q, caller.TenantID, daysStr, appID, scriptID).Scan(
		&resp.Completed,
		&resp.Errors,
		&resp.Limits,
		&resp.Cancelled,
		&resp.AvgDurationMs,
		&resp.MaxDurationMs,
		&resp.Tokens,
		&resp.CostCents,
	)
	if err != nil {
		return nil, fmt.Errorf("aggregating script stats: %w", err)
	}
	return resp, nil
}
