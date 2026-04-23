// Package builder: builder_runner.go is the TaskRunner implementation for
// task_type='builder_script'. The unified scheduler claim loop (see
// internal/scheduler/scheduler.go + runner.go) hands us a claimed tasks
// row whose config JSONB carries {"script_id","fn_name"}; we resolve the
// owning app, re-check the creator's admin status (demoted admins have
// their schedules deactivated rather than running under stale privilege),
// and hand off to invokeRunScript — the same keystone run_script uses for
// manual invocations.
//
// Why a separate file:
//   - Keeps the scheduler<->builder coupling visible in one place; the
//     rest of the builder package doesn't need to know about the
//     scheduler at all.
//   - Tests can construct a builderRunner directly with a stub deps
//     bundle to exercise the full run flow without going through the
//     process-global scheduler.RegisterTaskRunner path.
package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/robfig/cron/v3"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/scheduler"
	"github.com/mrdon/kit/internal/services"
)

// builderRunner implements scheduler.TaskRunner for task_type='builder_script'.
// It holds the same scriptRunDeps that the manual run_script handler uses,
// so a scheduled run sees identical engine behaviour — a scheduled tick
// is literally "someone invoking run_script on your behalf."
type builderRunner struct {
	pool *pgxpool.Pool
	deps *scriptRunDeps
}

// WireTaskRunners installs the builder's TaskRunner into the scheduler.
// Idempotent — calling twice just replaces the previous registration.
// Passing a nil pool/deps clears the registration so the scheduler stops
// claiming builder_script rows (useful in tests).
func WireTaskRunners(pool *pgxpool.Pool, deps *scriptRunDeps) {
	if pool == nil || deps == nil || deps.Engine == nil {
		scheduler.ClearTaskRunnerForTest(string(models.TaskTypeBuilderScript))
		return
	}
	scheduler.RegisterTaskRunner(&builderRunner{pool: pool, deps: deps})
}

func (r *builderRunner) TaskType() string {
	return string(models.TaskTypeBuilderScript)
}

// Run is the entry point the scheduler calls for each claimed
// task_type='builder_script' row. Re-checks the creator's admin status,
// resolves the script, invokes the same keystone as run_script, and
// advances next_run_at from the cron expression.
func (r *builderRunner) Run(ctx context.Context, task *models.Task) error {
	if r.deps == nil || r.deps.Engine == nil {
		return errors.New("builder runner: engine not wired")
	}

	scriptID, fnName, err := parseBuilderScriptTaskConfig(task)
	if err != nil {
		slog.Error("builder_script task has malformed config — deactivating",
			"task_id", task.ID, "error", err)
		r.deactivate(ctx, task, "malformed config")
		return nil
	}

	// Re-check creator admin status at claim time. Demoted admins have
	// their schedules deactivated — we don't want a paused admin's
	// script to keep running forever.
	user, err := models.GetUserByID(ctx, r.pool, task.TenantID, task.CreatedBy)
	if err != nil {
		slog.Error("loading builder_script creator", "task_id", task.ID, "error", err)
		r.advanceNextRun(ctx, task)
		return nil
	}
	if user == nil {
		slog.Warn("builder_script creator no longer exists — deactivating",
			"task_id", task.ID, "creator_id", task.CreatedBy)
		r.deactivate(ctx, task, "creator no longer exists")
		return nil
	}
	roles, err := models.GetUserRoleNames(ctx, r.pool, task.TenantID, user.ID, nil)
	if err != nil {
		slog.Error("loading builder_script creator roles", "task_id", task.ID, "error", err)
		r.advanceNextRun(ctx, task)
		return nil
	}
	if !slices.Contains(roles, models.RoleAdmin) {
		slog.Warn("builder_script creator is no longer admin — deactivating",
			"task_id", task.ID, "creator_id", task.CreatedBy)
		r.deactivate(ctx, task, "creator no longer admin")
		return nil
	}

	// Resolve script + owning app to populate the Caller's app scope.
	var builderAppID uuid.UUID
	var appName string
	var scriptName string
	err = r.pool.QueryRow(ctx, `
		SELECT s.builder_app_id, ba.name, s.name
		FROM scripts s
		JOIN builder_apps ba ON ba.id = s.builder_app_id
		WHERE s.tenant_id = $1 AND s.id = $2
	`, task.TenantID, scriptID).Scan(&builderAppID, &appName, &scriptName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("builder_script task references missing script — deactivating",
				"task_id", task.ID, "script_id", scriptID)
			r.deactivate(ctx, task, "script no longer exists")
			return nil
		}
		slog.Error("loading script for scheduled run", "task_id", task.ID, "error", err)
		r.advanceNextRun(ctx, task)
		return fmt.Errorf("loading script: %w", err)
	}

	tz := user.Timezone
	if tz == "" {
		tz = "UTC"
	}

	caller := &services.Caller{
		TenantID: task.TenantID,
		UserID:   user.ID,
		Roles:    roles,
		IsAdmin:  true, // already re-checked above via roles
		Timezone: tz,
	}

	// invokeRunScript inserts its own script_runs row with
	// triggered_by='manual'; override via a post-insert UPDATE so the
	// audit trail distinguishes scheduled from manual runs. We do this
	// after the run completes so the row exists to UPDATE.
	resp, runErr := invokeRunScript(ctx, r.pool, caller, r.deps, appName, scriptName, fnName, map[string]any{}, nil)
	if runErr != nil {
		slog.Error("builder_script run failed", "task_id", task.ID, "error", runErr)
	}
	if resp != nil {
		_, upErr := r.pool.Exec(ctx, `
			UPDATE script_runs
			SET triggered_by = $1
			WHERE tenant_id = $2 AND id = $3
		`, TriggerSchedule, task.TenantID, resp.RunID)
		if upErr != nil {
			slog.Warn("tagging script_run as scheduled", "run_id", resp.RunID, "error", upErr)
		}
	}

	r.advanceNextRun(ctx, task)
	return nil
}

// parseBuilderScriptTaskConfig extracts (script_id, fn_name) from a
// task.Config JSONB payload. The schedule_script meta-tool writes this
// exact shape; a parse error means the row was tampered with (not a
// pre-existing row left by an older binary, since this column was added
// in the same migration that introduced task_type='builder_script').
func parseBuilderScriptTaskConfig(task *models.Task) (uuid.UUID, string, error) {
	if len(task.Config) == 0 {
		return uuid.Nil, "", errors.New("config is empty")
	}
	var cfg struct {
		ScriptID string `json:"script_id"`
		FnName   string `json:"fn_name"`
	}
	if err := json.Unmarshal(task.Config, &cfg); err != nil {
		return uuid.Nil, "", fmt.Errorf("parsing config: %w", err)
	}
	id, err := uuid.Parse(cfg.ScriptID)
	if err != nil {
		return uuid.Nil, "", fmt.Errorf("parsing script_id: %w", err)
	}
	if cfg.FnName == "" {
		return uuid.Nil, "", errors.New("fn_name is empty")
	}
	return id, cfg.FnName, nil
}

// advanceNextRun computes the next cron tick and flips the task back to
// status='active' so the scheduler picks it up again. A bad cron (data
// corruption, since we validated at schedule_script time) deactivates
// the row rather than looping forever.
func (r *builderRunner) advanceNextRun(ctx context.Context, task *models.Task) {
	loc, err := time.LoadLocation(task.Timezone)
	if err != nil {
		slog.Error("builder_script bad timezone — deactivating",
			"task_id", task.ID, "timezone", task.Timezone, "error", err)
		r.deactivate(ctx, task, "invalid timezone")
		return
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	sched, err := parser.Parse(task.CronExpr)
	if err != nil {
		slog.Error("builder_script bad cron — deactivating",
			"task_id", task.ID, "cron", task.CronExpr, "error", err)
		r.deactivate(ctx, task, "invalid cron")
		return
	}
	nextRun := sched.Next(time.Now().In(loc)).UTC()

	_, err = r.pool.Exec(ctx, `
		UPDATE tasks
		SET status = $3, next_run_at = $4, last_run_at = now()
		WHERE tenant_id = $1 AND id = $2
	`, task.TenantID, task.ID, models.TaskStatusActive, nextRun)
	if err != nil {
		slog.Error("updating builder_script next_run_at", "task_id", task.ID, "error", err)
	}
}

// deactivate flips a task_type='builder_script' row to status='inactive'.
// The row survives (admins see it in list_schedules with active=false)
// so they can understand what was paused and why. The reason is logged,
// not persisted — a last_error column could surface it in v0.2.
func (r *builderRunner) deactivate(ctx context.Context, task *models.Task, reason string) {
	_, err := r.pool.Exec(ctx, `
		UPDATE tasks
		SET status = $3
		WHERE tenant_id = $1 AND id = $2
	`, task.TenantID, task.ID, models.TaskStatusInactive)
	if err != nil {
		slog.Error("deactivating builder_script task", "task_id", task.ID, "error", err)
		return
	}
	slog.Info("builder_script task deactivated", "task_id", task.ID, "reason", reason)
}

// builderRunnerForTest exposes the runner constructor for integration
// tests in this package that want to drive Run directly without going
// through the global scheduler registration.
func builderRunnerForTest(pool *pgxpool.Pool, deps *scriptRunDeps) *builderRunner {
	return &builderRunner{pool: pool, deps: deps}
}
