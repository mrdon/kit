// Package builder: meta_schedules.go implements the three schedule-CRUD
// meta-tools admins use to hook a script function onto a cron expression.
// Schedules live on the generic `jobs` table as job_type='builder_script'
// rows; the scheduler's unified claim loop dispatches them through
// builder_runner.go. This file only owns the DB shape + validation so
// schedule mistakes (bad cron, unknown script) are caught at admin time,
// not at 03:00 when the tick fires.
//
// Contract summary:
//
//	app_schedule_script(app, script, fn, cron, timezone="UTC")
//	    -> { id, app, script, fn, cron, timezone, next_run_at, active }
//	app_unschedule_script(app, script, fn)
//	    -> { unscheduled: true }
//	app_list_schedules(app=None)
//	    -> [ { id, app, script, fn, cron, timezone, next_run_at, active }, ... ]
//
// Why `flip status=inactive` rather than DELETE on unschedule:
//   - Keeps history for "why did this fire last Tuesday but not Wednesday?"
//   - The partial unique index on (tenant_id, script_id, fn_name) WHERE
//     status='active' means a later app_schedule_script for the same
//     (script, fn) can revive the inactive row rather than erroring.
//
// Admin-only: every handler calls guardAdmin. The MCP tool surface is the
// same — non-admin MCP callers get ErrForbidden via the shared guard.
package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/robfig/cron/v3"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
)

// metaScheduleTools enumerates the three schedule meta-tools. Shape matches
// metaScriptTools so the services catalog + MCP registration pick them up
// through App.ToolMetas.
var metaScheduleTools = []services.ToolMeta{
	{
		Name:        "app_schedule_script",
		Description: "Schedule a function in a builder-app script to run on a cron expression. Re-scheduling the same (script, fn) updates cron + timezone and flips active=true. Minimum interval is 1 hour.",
		Schema: services.PropsReq(map[string]any{
			"app":      services.Field("string", "Builder app name that owns the script"),
			"script":   services.Field("string", "Script identifier"),
			"fn":       services.Field("string", "Function inside the script to invoke on each tick"),
			"cron":     services.Field("string", "5-field cron expression (e.g. '0 9 * * 1' for Monday 9am). Must fire no more than once per hour."),
			"timezone": services.Field("string", "IANA timezone for the cron. Defaults to the caller's timezone."),
		}, "app", "script", "fn", "cron"),
		AdminOnly: true,
	},
	{
		Name:        "app_unschedule_script",
		Description: "Deactivate a scheduled app-script. The row is preserved with active=false so history + cron expression survive.",
		Schema: services.PropsReq(map[string]any{
			"app":    services.Field("string", "Builder app name"),
			"script": services.Field("string", "Script identifier"),
			"fn":     services.Field("string", "Function name"),
		}, "app", "script", "fn"),
		AdminOnly: true,
	},
	{
		Name:        "app_list_schedules",
		Description: "List scheduled app-scripts in the tenant. Optional app filter. Returns active and inactive entries so admins can audit what is paused.",
		Schema: services.Props(map[string]any{
			"app": services.Field("string", "Builder app name to filter by (optional)"),
		}),
		AdminOnly: true,
	},
}

// MetaScheduleTools returns the schedule meta-tools' metadata for
// App.ToolMetas so both agent + MCP registration wire them automatically.
func MetaScheduleTools() []services.ToolMeta { return metaScheduleTools }

// scheduleDTO is the JSON shape app_schedule_script / app_list_schedules return.
// Kept narrow on purpose — tenant_id is internal, and the script name +
// app name surface instead of raw UUIDs so the LLM can refer back to
// schedules by human-readable identifier.
type scheduleDTO struct {
	ID        uuid.UUID `json:"id"`
	App       string    `json:"app"`
	Script    string    `json:"script"`
	Fn        string    `json:"fn"`
	Cron      string    `json:"cron"`
	Timezone  string    `json:"timezone"`
	NextRunAt time.Time `json:"next_run_at"`
	Active    bool      `json:"active"`
}

// metaScheduleAgentHandler returns the schedule handler for a given tool
// name. Nil for unknown names so app.go's registration loop skips them.
func metaScheduleAgentHandler(name string) func(ec *execContextLike, input json.RawMessage) (string, error) {
	switch name {
	case "app_schedule_script":
		return handleScheduleScript
	case "app_unschedule_script":
		return handleUnscheduleScript
	case "app_list_schedules":
		return handleListSchedules
	default:
		return nil
	}
}

func handleScheduleScript(ec *execContextLike, input json.RawMessage) (string, error) {
	m, err := parseInput(input)
	if err != nil {
		return "", err
	}
	appName, err := argString(m, "app")
	if err != nil {
		return "", err
	}
	scriptName, err := argString(m, "script")
	if err != nil {
		return "", err
	}
	fn, err := argString(m, "fn")
	if err != nil {
		return "", err
	}
	cronExpr, err := argString(m, "cron")
	if err != nil {
		return "", err
	}
	tz, _ := argOptionalString(m, "timezone")
	if tz == "" {
		tz = ec.Caller.Timezone
	}
	if tz == "" {
		tz = "UTC"
	}

	dto, err := scheduleScript(ec.Ctx, ec.Pool, ec.Caller, appName, scriptName, fn, cronExpr, tz)
	if err != nil {
		return "", err
	}
	return formatToolResult(dto)
}

func handleUnscheduleScript(ec *execContextLike, input json.RawMessage) (string, error) {
	m, err := parseInput(input)
	if err != nil {
		return "", err
	}
	appName, err := argString(m, "app")
	if err != nil {
		return "", err
	}
	scriptName, err := argString(m, "script")
	if err != nil {
		return "", err
	}
	fn, err := argString(m, "fn")
	if err != nil {
		return "", err
	}
	if err := unscheduleScript(ec.Ctx, ec.Pool, ec.Caller, appName, scriptName, fn); err != nil {
		return "", err
	}
	return formatToolResult(map[string]any{"unscheduled": true})
}

func handleListSchedules(ec *execContextLike, input json.RawMessage) (string, error) {
	m, err := parseInput(input)
	if err != nil {
		return "", err
	}
	appName, _ := argOptionalString(m, "app")
	out, err := listSchedules(ec.Ctx, ec.Pool, ec.Caller, appName)
	if err != nil {
		return "", err
	}
	return formatToolResult(out)
}

// parseScheduleCron validates a cron expression using the same 5-field
// parser the jobs table uses (so app_schedule_script ↔ CreateJob stay
// consistent). Returns a cron.Schedule the caller can use to compute
// next_run_at.
func parseScheduleCron(expr, tz string) (cron.Schedule, *time.Location, error) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, nil, fmt.Errorf("loading timezone %q: %w", tz, err)
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	sched, err := parser.Parse(expr)
	if err != nil {
		return nil, nil, fmt.Errorf("parsing cron %q: %w", expr, err)
	}
	// Reject sub-hourly schedules. Each tick can spawn LLM calls, and tenants
	// have a daily spend cap; a fat-fingered "* * * * *" would burn through
	// it in one afternoon. Catching it at schedule time is cheap and loud.
	now := time.Now().In(loc)
	t1 := sched.Next(now)
	t2 := sched.Next(t1)
	if t2.Sub(t1) < time.Hour {
		return nil, nil, fmt.Errorf("cron %q fires more often than once per hour; minimum interval is 1h", expr)
	}
	return sched, loc, nil
}

// scheduleDescription is the human-readable label we persist in
// jobs.description for a builder_script row. Kept short so it fits in
// log lines without blowing up the output.
func scheduleDescription(appName, scriptName, fn string) string {
	return fmt.Sprintf("builder: %s/%s.%s", appName, scriptName, fn)
}

// scheduleScript inserts (or revives) a job_type='builder_script' row.
// The flow:
//
//  1. Resolve (tenant, app, script) to script.ID.
//  2. Validate cron + timezone so we never write a row the scheduler
//     can't parse.
//  3. Pre-check: an active row for the same (script, fn) is an error —
//     double-firing is worse than a loud admin-facing error. An inactive
//     row revives via UpsertBuilderScriptTask.
func scheduleScript(
	ctx context.Context,
	pool *pgxpool.Pool,
	caller *services.Caller,
	appName, scriptName, fn, cronExpr, tz string,
) (*scheduleDTO, error) {
	if err := guardAdmin(caller); err != nil {
		return nil, err
	}
	if fn == "" {
		return nil, errors.New("fn must be non-empty")
	}

	app, err := loadBuilderAppByName(ctx, pool, caller.TenantID, appName)
	if err != nil {
		return nil, err
	}
	script, err := loadScriptByName(ctx, pool, caller.TenantID, app.ID, scriptName)
	if err != nil {
		return nil, err
	}

	sched, loc, err := parseScheduleCron(cronExpr, tz)
	if err != nil {
		return nil, err
	}
	nextRun := sched.Next(time.Now().In(loc)).UTC()

	// Pre-check: if a row already exists and is active, refuse. Reactivating
	// an inactive row is fine (admin flipped it back on). This mirrors the
	// plan's "error vs. skip" — duplicate ACTIVE schedules would double-fire.
	var existingActive bool
	err = pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM jobs
			WHERE tenant_id = $1
			  AND job_type = $2
			  AND status = $3
			  AND config->>'script_id' = $4
			  AND config->>'fn_name'   = $5
		)
	`, caller.TenantID, models.JobTypeBuilderScript, models.JobStatusActive,
		script.ID.String(), fn).Scan(&existingActive)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("checking existing schedule: %w", err)
	}
	if existingActive {
		return nil, fmt.Errorf("script %q fn %q is already scheduled in app %q", scriptName, fn, appName)
	}

	id, err := models.UpsertBuilderScriptTask(
		ctx, pool, caller.TenantID, caller.UserID,
		script.ID, fn, scheduleDescription(appName, scriptName, fn),
		cronExpr, tz, nextRun,
	)
	if err != nil {
		return nil, fmt.Errorf("inserting scheduled script: %w", err)
	}

	return &scheduleDTO{
		ID:        id,
		App:       appName,
		Script:    scriptName,
		Fn:        fn,
		Cron:      cronExpr,
		Timezone:  tz,
		NextRunAt: nextRun,
		Active:    true,
	}, nil
}

// unscheduleScript flips status=inactive. The row survives because cron +
// timezone + the id itself are useful audit anchors even once paused.
// Calling unschedule on a row that never existed returns "not scheduled"
// rather than silently succeeding — the LLM should know when it was
// talking about a schedule that doesn't exist.
func unscheduleScript(
	ctx context.Context,
	pool *pgxpool.Pool,
	caller *services.Caller,
	appName, scriptName, fn string,
) error {
	if err := guardAdmin(caller); err != nil {
		return err
	}

	app, err := loadBuilderAppByName(ctx, pool, caller.TenantID, appName)
	if err != nil {
		return err
	}
	script, err := loadScriptByName(ctx, pool, caller.TenantID, app.ID, scriptName)
	if err != nil {
		return err
	}

	flipped, err := models.DeactivateBuilderScriptTask(ctx, pool, caller.TenantID, script.ID, fn)
	if err != nil {
		return fmt.Errorf("unscheduling: %w", err)
	}
	if !flipped {
		return fmt.Errorf("script %q fn %q is not scheduled in app %q", scriptName, fn, appName)
	}
	return nil
}

// listSchedules returns all builder_script jobs for a tenant, optionally
// filtered by builder app. Orders by (app, script, fn) so output is
// stable across calls.
func listSchedules(
	ctx context.Context,
	pool *pgxpool.Pool,
	caller *services.Caller,
	appName string,
) ([]scheduleDTO, error) {
	if err := guardAdmin(caller); err != nil {
		return nil, err
	}

	baseSQL := `
		SELECT t.id, ba.name, s.name, t.config->>'fn_name',
		       t.cron_expr, t.timezone, t.next_run_at,
		       (t.status = $2) AS active
		FROM jobs t
		JOIN scripts s       ON s.id  = (t.config->>'script_id')::uuid
		                       AND s.tenant_id = t.tenant_id
		JOIN builder_apps ba ON ba.id = s.builder_app_id
		                       AND ba.tenant_id = t.tenant_id
		WHERE t.tenant_id = $1 AND t.job_type = $3
	`

	var (
		rows pgx.Rows
		err  error
	)
	if appName == "" {
		rows, err = pool.Query(ctx, baseSQL+`
			ORDER BY ba.name, s.name, t.config->>'fn_name'
		`, caller.TenantID, models.JobStatusActive, models.JobTypeBuilderScript)
	} else {
		app, ferr := loadBuilderAppByName(ctx, pool, caller.TenantID, appName)
		if ferr != nil {
			return nil, ferr
		}
		rows, err = pool.Query(ctx, baseSQL+`
			  AND s.builder_app_id = $4
			ORDER BY ba.name, s.name, t.config->>'fn_name'
		`, caller.TenantID, models.JobStatusActive, models.JobTypeBuilderScript, app.ID)
	}
	if err != nil {
		return nil, fmt.Errorf("listing schedules: %w", err)
	}
	defer rows.Close()

	out := make([]scheduleDTO, 0)
	for rows.Next() {
		var dto scheduleDTO
		if err := rows.Scan(&dto.ID, &dto.App, &dto.Script, &dto.Fn, &dto.Cron, &dto.Timezone, &dto.NextRunAt, &dto.Active); err != nil {
			return nil, fmt.Errorf("scanning schedule: %w", err)
		}
		out = append(out, dto)
	}
	return out, rows.Err()
}
