// Package builder: builder_runner.go is the JobRunner implementation for
// job_type='builder_script'. The unified scheduler claim loop (see
// internal/scheduler/scheduler.go + runner.go) hands us a claimed jobs
// row whose config JSONB carries {"script_id","fn_name"}; we resolve the
// owning app, re-check the creator's admin status (demoted admins have
// their schedules deactivated rather than running under stale privilege),
// and hand off to invokeRunScript — the same keystone app_run_script uses for
// manual invocations.
//
// Why a separate file:
//   - Keeps the scheduler<->builder coupling visible in one place; the
//     rest of the builder package doesn't need to know about the
//     scheduler at all.
//   - Tests can construct a builderRunner directly with a stub deps
//     bundle to exercise the full run flow without going through the
//     process-global scheduler.RegisterJobRunner path.
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

// builderRunner implements scheduler.JobRunner for job_type='builder_script'.
// It holds the same scriptRunDeps that the manual app_run_script handler uses,
// so a scheduled run sees identical engine behaviour — a scheduled tick
// is literally "someone invoking app_run_script on your behalf."
type builderRunner struct {
	pool *pgxpool.Pool
	deps *scriptRunDeps
}

// WireJobRunners installs the builder's JobRunner into the scheduler.
// Idempotent — calling twice just replaces the previous registration.
// Passing a nil pool/deps clears the registration so the scheduler stops
// claiming builder_script rows (useful in tests).
func WireJobRunners(pool *pgxpool.Pool, deps *scriptRunDeps) {
	if pool == nil || deps == nil || deps.Engine == nil {
		scheduler.ClearJobRunnerForTest(string(models.JobTypeBuilderScript))
		return
	}
	scheduler.RegisterJobRunner(&builderRunner{pool: pool, deps: deps})
}

func (r *builderRunner) JobType() string {
	return string(models.JobTypeBuilderScript)
}

// Run is the entry point the scheduler calls for each claimed
// job_type='builder_script' row. Re-checks the creator's admin status,
// resolves the script, invokes the same keystone as app_run_script, and
// advances next_run_at from the cron expression.
func (r *builderRunner) Run(ctx context.Context, job *models.Job) error {
	if r.deps == nil || r.deps.Engine == nil {
		return errors.New("builder runner: engine not wired")
	}

	scriptID, fnName, err := parseBuilderScriptTaskConfig(job)
	if err != nil {
		slog.Error("builder_script job has malformed config — deactivating",
			"job_id", job.ID, "error", err)
		r.deactivate(ctx, job, "malformed config")
		return nil
	}

	// Re-check creator admin status at claim time. Demoted admins have
	// their schedules deactivated — we don't want a paused admin's
	// script to keep running forever.
	user, err := models.GetUserByID(ctx, r.pool, job.TenantID, job.CreatedBy)
	if err != nil {
		slog.Error("loading builder_script creator", "job_id", job.ID, "error", err)
		r.advanceNextRun(ctx, job)
		return nil
	}
	if user == nil {
		slog.Warn("builder_script creator no longer exists — deactivating",
			"job_id", job.ID, "creator_id", job.CreatedBy)
		r.deactivate(ctx, job, "creator no longer exists")
		return nil
	}
	roles, err := models.GetUserRoleNames(ctx, r.pool, job.TenantID, user.ID, nil)
	if err != nil {
		slog.Error("loading builder_script creator roles", "job_id", job.ID, "error", err)
		r.advanceNextRun(ctx, job)
		return nil
	}
	if !slices.Contains(roles, models.RoleAdmin) {
		slog.Warn("builder_script creator is no longer admin — deactivating",
			"job_id", job.ID, "creator_id", job.CreatedBy)
		r.deactivate(ctx, job, "creator no longer admin")
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
	`, job.TenantID, scriptID).Scan(&builderAppID, &appName, &scriptName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("builder_script job references missing script — deactivating",
				"job_id", job.ID, "script_id", scriptID)
			r.deactivate(ctx, job, "script no longer exists")
			return nil
		}
		slog.Error("loading script for scheduled run", "job_id", job.ID, "error", err)
		r.advanceNextRun(ctx, job)
		return fmt.Errorf("loading script: %w", err)
	}

	tz := user.Timezone
	if tz == "" {
		tz = "UTC"
	}

	caller := &services.Caller{
		TenantID: job.TenantID,
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
		slog.Error("builder_script run failed", "job_id", job.ID, "error", runErr)
	}
	if resp != nil {
		_, upErr := r.pool.Exec(ctx, `
			UPDATE script_runs
			SET triggered_by = $1
			WHERE tenant_id = $2 AND id = $3
		`, TriggerSchedule, job.TenantID, resp.RunID)
		if upErr != nil {
			slog.Warn("tagging script_run as scheduled", "run_id", resp.RunID, "error", upErr)
		}
	}

	r.advanceNextRun(ctx, job)
	return nil
}

// parseBuilderScriptTaskConfig extracts (script_id, fn_name) from a
// job.Config JSONB payload. The app_schedule_script meta-tool writes this
// exact shape; a parse error means the row was tampered with (not a
// pre-existing row left by an older binary, since this column was added
// in the same migration that introduced job_type='builder_script').
func parseBuilderScriptTaskConfig(job *models.Job) (uuid.UUID, string, error) {
	if len(job.Config) == 0 {
		return uuid.Nil, "", errors.New("config is empty")
	}
	var cfg struct {
		ScriptID string `json:"script_id"`
		FnName   string `json:"fn_name"`
	}
	if err := json.Unmarshal(job.Config, &cfg); err != nil {
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

// advanceNextRun computes the next cron tick and flips the job back to
// status='active' so the scheduler picks it up again. A bad cron (data
// corruption, since we validated at app_schedule_script time) deactivates
// the row rather than looping forever.
func (r *builderRunner) advanceNextRun(ctx context.Context, job *models.Job) {
	loc, err := time.LoadLocation(job.Timezone)
	if err != nil {
		slog.Error("builder_script bad timezone — deactivating",
			"job_id", job.ID, "timezone", job.Timezone, "error", err)
		r.deactivate(ctx, job, "invalid timezone")
		return
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	sched, err := parser.Parse(job.CronExpr)
	if err != nil {
		slog.Error("builder_script bad cron — deactivating",
			"job_id", job.ID, "cron", job.CronExpr, "error", err)
		r.deactivate(ctx, job, "invalid cron")
		return
	}
	nextRun := sched.Next(time.Now().In(loc)).UTC()

	_, err = r.pool.Exec(ctx, `
		UPDATE jobs
		SET status = $3, next_run_at = $4, last_run_at = now()
		WHERE tenant_id = $1 AND id = $2
	`, job.TenantID, job.ID, models.JobStatusActive, nextRun)
	if err != nil {
		slog.Error("updating builder_script next_run_at", "job_id", job.ID, "error", err)
	}
}

// deactivate flips a job_type='builder_script' row to status='inactive'.
// The row survives (admins see it in app_list_schedules with active=false)
// so they can understand what was paused and why. The reason is logged,
// not persisted — a last_error column could surface it in v0.2.
func (r *builderRunner) deactivate(ctx context.Context, job *models.Job, reason string) {
	_, err := r.pool.Exec(ctx, `
		UPDATE jobs
		SET status = $3
		WHERE tenant_id = $1 AND id = $2
	`, job.TenantID, job.ID, models.JobStatusInactive)
	if err != nil {
		slog.Error("deactivating builder_script job", "job_id", job.ID, "error", err)
		return
	}
	slog.Info("builder_script job deactivated", "job_id", job.ID, "reason", reason)
}

// builderRunnerForTest exposes the runner constructor for integration
// tests in this package that want to drive Run directly without going
// through the global scheduler registration.
func builderRunnerForTest(pool *pgxpool.Pool, deps *scriptRunDeps) *builderRunner {
	return &builderRunner{pool: pool, deps: deps}
}
