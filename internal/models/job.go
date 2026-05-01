package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/robfig/cron/v3"
)

// JobStatus is the lifecycle state of a job row.
type JobStatus string

const (
	JobStatusActive    JobStatus = "active"    // due to run next
	JobStatusRunning   JobStatus = "running"   // claimed by a scheduler, currently executing
	JobStatusCompleted JobStatus = "completed" // one-time job that finished
	JobStatusInactive  JobStatus = "inactive"  // paused / unscheduled, row preserved for audit + revival
)

// JobType discriminates between native handlers and full agent runs.
type JobType string

const (
	JobTypeAgent         JobType = "agent"
	JobTypeBuiltin       JobType = "builtin"
	JobTypeBuilderScript JobType = "builder_script" // scheduled builder script; config carries {script_id, fn_name}
)

// Tier names persisted in jobs.model. Picked at create_task time by a
// Haiku classifier pass and threaded into agent.RunInput.Model so the
// scheduler honours the per-job choice. Kept as short tier names (not
// full Anthropic model IDs) so a future pricing/ID shift only updates
// ModelIDFor, not every row.
const (
	JobModelHaiku  = "haiku"
	JobModelSonnet = "sonnet"
)

// ModelIDFor maps a tier name to the Anthropic Messages API model ID.
// Empty / unknown values fall back to the Haiku ID so callers that don't
// set Model on RunInput keep today's behaviour.
func ModelIDFor(tier string) string {
	switch tier {
	case JobModelSonnet:
		return "claude-sonnet-4-6"
	default:
		return "claude-haiku-4-5-20251001"
	}
}

type Job struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	CreatedBy   uuid.UUID
	Description string
	CronExpr    string
	Timezone    string
	ChannelID   string
	RunOnce     bool
	JobType     JobType
	Status      JobStatus
	NextRunAt   time.Time
	LastRunAt   *time.Time
	LastError   *string
	// Config is a job_type-specific JSONB payload. For
	// job_type='builder_script' it carries {"script_id","fn_name"}.
	// Nil for agent/builtin jobs where the fixed columns are sufficient.
	Config []byte
	// ResumeSessionID is set by ResolveDecision to tell the scheduler
	// "resume into this session on your next run of this job." The
	// scheduler consumes and clears it at claim time. Nil for jobs not
	// waiting on a decision.
	ResumeSessionID *uuid.UUID
	// Model is the tier name ("haiku" or "sonnet") the scheduler should
	// run this job under. Picked once by a Haiku classifier at create
	// time; builtin / builder_script rows just carry the default.
	Model string
	// SkillID, when non-nil, points at the skill the scheduler should
	// load and execute instead of running Description as a free-form
	// prompt. FK with ON DELETE CASCADE — deleting the skill removes
	// any jobs built around it. The runtime resolves the current slug
	// name at fire time so skill renames don't break the prompt.
	SkillID   *uuid.UUID
	CreatedAt time.Time
}

// NextCronRun computes the next run time for a cron expression in the given timezone.
func NextCronRun(cronExpr, tz string, after time.Time) (time.Time, error) {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.Time{}, fmt.Errorf("loading timezone %q: %w", tz, err)
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	sched, err := parser.Parse(cronExpr)
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing cron %q: %w", cronExpr, err)
	}
	return sched.Next(after.In(loc)).UTC(), nil
}

// CreateJob creates a scheduled job with a scope row in its own transaction.
// For recurring jobs, provide cronExpr. For one-time jobs, provide runAt and set runOnce=true.
// roleID/userID identify the scope target (both nil = tenant-wide).
// model is the tier name ("haiku" | "sonnet") chosen by the classifier; empty
// defaults to Haiku at the DB level.
func CreateJob(ctx context.Context, pool *pgxpool.Pool, tenantID, createdBy uuid.UUID, description, cronExpr, tz, channelID, model string, skillID *uuid.UUID, runOnce bool, runAt *time.Time, roleID, userID *uuid.UUID) (*Job, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	job, err := CreateJobTx(ctx, tx, tenantID, createdBy, description, cronExpr, tz, channelID, model, skillID, runOnce, runAt, roleID, userID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("committing: %w", err)
	}
	return job, nil
}

// CreateJobTx inserts a job and its scope row into the supplied transaction.
// The caller is responsible for Begin / Commit / Rollback. Use this when job
// creation must be atomic with other writes (e.g. resolving a decision card).
func CreateJobTx(ctx context.Context, tx pgx.Tx, tenantID, createdBy uuid.UUID, description, cronExpr, tz, channelID, model string, skillID *uuid.UUID, runOnce bool, runAt *time.Time, roleID, userID *uuid.UUID) (*Job, error) {
	var nextRun time.Time
	if runOnce && runAt != nil {
		nextRun = runAt.UTC()
	} else {
		var err error
		nextRun, err = NextCronRun(cronExpr, tz, time.Now())
		if err != nil {
			return nil, err
		}
	}

	if model == "" {
		model = JobModelHaiku
	}

	job := &Job{}
	jobID := uuid.New()
	err := tx.QueryRow(ctx, `
		INSERT INTO jobs (id, tenant_id, created_by, description, cron_expr, timezone, channel_id, run_once, next_run_at, model, skill_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, tenant_id, created_by, description, cron_expr, timezone, channel_id, run_once, job_type, status, next_run_at, last_run_at, last_error, config, resume_session_id, model, skill_id, created_at
	`, jobID, tenantID, createdBy, description, cronExpr, tz, channelID, runOnce, nextRun, model, skillID).Scan(
		&job.ID, &job.TenantID, &job.CreatedBy, &job.Description, &job.CronExpr,
		&job.Timezone, &job.ChannelID, &job.RunOnce, &job.JobType, &job.Status, &job.NextRunAt, &job.LastRunAt, &job.LastError, &job.Config, &job.ResumeSessionID, &job.Model, &job.SkillID, &job.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("creating job: %w", err)
	}

	scopeID, err := GetOrCreateScopeTx(ctx, tx, tenantID, roleID, userID)
	if err != nil {
		return nil, fmt.Errorf("get-or-create scope: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO job_scopes (tenant_id, job_id, scope_id)
		VALUES ($1, $2, $3)
	`, tenantID, jobID, scopeID)
	if err != nil {
		return nil, fmt.Errorf("creating job scope: %w", err)
	}
	return job, nil
}

// GetJob returns a single job by ID.
func GetJob(ctx context.Context, pool *pgxpool.Pool, tenantID, jobID uuid.UUID) (*Job, error) {
	return getJobRow(ctx, pool, tenantID, jobID)
}

// GetJobTx is GetJob but runs inside a transaction. Used when job
// lookup must be atomic with a subsequent update (e.g. requeue during
// decision resolution).
func GetJobTx(ctx context.Context, tx pgx.Tx, tenantID, jobID uuid.UUID) (*Job, error) {
	return getJobRow(ctx, tx, tenantID, jobID)
}

type jobRowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func getJobRow(ctx context.Context, q jobRowQuerier, tenantID, jobID uuid.UUID) (*Job, error) {
	t := &Job{}
	err := q.QueryRow(ctx, `
		SELECT id, tenant_id, created_by, description, cron_expr, timezone, channel_id, run_once, job_type, status, next_run_at, last_run_at, last_error, config, resume_session_id, model, skill_id, created_at
		FROM jobs WHERE tenant_id = $1 AND id = $2
	`, tenantID, jobID).Scan(
		&t.ID, &t.TenantID, &t.CreatedBy, &t.Description, &t.CronExpr,
		&t.Timezone, &t.ChannelID, &t.RunOnce, &t.JobType, &t.Status, &t.NextRunAt, &t.LastRunAt, &t.LastError, &t.Config, &t.ResumeSessionID, &t.Model, &t.SkillID, &t.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // not found is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("getting job: %w", err)
	}
	return t, nil
}

// ListJobsForContext returns jobs visible to the user via scope filtering.
func ListJobsForContext(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID, roleIDs []uuid.UUID) ([]Job, error) {
	scopeSQL, scopeArgs := ScopeFilterIDs("sc", 2, userID, roleIDs)
	args := append([]any{tenantID}, scopeArgs...)
	rows, err := pool.Query(ctx, `
		SELECT DISTINCT t.id, t.tenant_id, t.created_by, t.description, t.cron_expr, t.timezone,
			t.channel_id, t.run_once, t.job_type, t.status, t.next_run_at, t.last_run_at, t.last_error, t.config, t.resume_session_id, t.model, t.skill_id, t.created_at
		FROM jobs t
		JOIN job_scopes ts ON ts.job_id = t.id AND ts.tenant_id = t.tenant_id
		JOIN scopes sc ON sc.id = ts.scope_id
		WHERE t.tenant_id = $1
		AND (`+scopeSQL+`)
		ORDER BY t.created_at
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("listing jobs: %w", err)
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		var t Job
		if err := rows.Scan(&t.ID, &t.TenantID, &t.CreatedBy, &t.Description, &t.CronExpr,
			&t.Timezone, &t.ChannelID, &t.RunOnce, &t.JobType, &t.Status, &t.NextRunAt, &t.LastRunAt, &t.LastError, &t.Config, &t.ResumeSessionID, &t.Model, &t.SkillID, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning job: %w", err)
		}
		jobs = append(jobs, t)
	}
	return jobs, rows.Err()
}

// UpdateJobDescription updates a job's description. Builtin jobs cannot be updated.
func UpdateJobDescription(ctx context.Context, pool *pgxpool.Pool, tenantID, jobID uuid.UUID, description string) error {
	tag, err := pool.Exec(ctx, `
		UPDATE jobs SET description = $3 WHERE tenant_id = $1 AND id = $2 AND job_type != $4
	`, tenantID, jobID, description, JobTypeBuiltin)
	if err != nil {
		return fmt.Errorf("updating job: %w", err)
	}
	if tag.RowsAffected() == 0 {
		t, err := GetJob(ctx, pool, tenantID, jobID)
		if err != nil {
			return err
		}
		if t == nil {
			return errors.New("job not found")
		}
		if t.JobType == JobTypeBuiltin {
			return errors.New("builtin jobs cannot be updated")
		}
	}
	return nil
}

// UpdateJobSkillID updates a job's skill_id. Pass nil to clear it
// (job falls back to Description as prompt). Builtin jobs cannot be
// updated.
func UpdateJobSkillID(ctx context.Context, pool *pgxpool.Pool, tenantID, jobID uuid.UUID, skillID *uuid.UUID) error {
	tag, err := pool.Exec(ctx, `
		UPDATE jobs SET skill_id = $3 WHERE tenant_id = $1 AND id = $2 AND job_type != $4
	`, tenantID, jobID, skillID, JobTypeBuiltin)
	if err != nil {
		return fmt.Errorf("updating job skill_id: %w", err)
	}
	if tag.RowsAffected() == 0 {
		t, err := GetJob(ctx, pool, tenantID, jobID)
		if err != nil {
			return err
		}
		if t == nil {
			return errors.New("job not found")
		}
		if t.JobType == JobTypeBuiltin {
			return errors.New("builtin jobs cannot be updated")
		}
	}
	return nil
}

// DeleteJob deletes a job and its scope rows (via CASCADE). Builtin jobs cannot be deleted.
func DeleteJob(ctx context.Context, pool *pgxpool.Pool, tenantID, jobID uuid.UUID) error {
	tag, err := pool.Exec(ctx, `DELETE FROM jobs WHERE tenant_id = $1 AND id = $2 AND job_type != $3`, tenantID, jobID, JobTypeBuiltin)
	if err != nil {
		return fmt.Errorf("deleting job: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Either not found or builtin — check which
		t, err := GetJob(ctx, pool, tenantID, jobID)
		if err != nil {
			return err
		}
		if t != nil && t.JobType == JobTypeBuiltin {
			return errors.New("builtin jobs cannot be deleted")
		}
	}
	return nil
}

// UpsertBuilderScriptTask creates (or revives) a job_type='builder_script'
// row. On conflict with the partial unique index on
// (tenant_id, config->>'script_id', config->>'fn_name') WHERE active, we
// instead look for an inactive row with the same (script_id, fn_name) and
// flip it back to active with the new cron/tz. Returns the job ID.
//
// scriptID + fnName end up in config JSONB; the scheduler's builder
// runner parses them back out at claim time.
func UpsertBuilderScriptTask(
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID, createdBy uuid.UUID,
	scriptID uuid.UUID, fnName, description, cronExpr, tz string,
	nextRun time.Time,
) (uuid.UUID, error) {
	configJSON := fmt.Sprintf(`{"script_id":%q,"fn_name":%q}`, scriptID.String(), fnName)

	// Revive path: if an inactive row already exists for (script_id, fn),
	// flip it back to active with the new cron.
	var existingID uuid.UUID
	err := pool.QueryRow(ctx, `
		SELECT id FROM jobs
		WHERE tenant_id = $1
		  AND job_type = $2
		  AND status = $3
		  AND config->>'script_id' = $4
		  AND config->>'fn_name'   = $5
	`, tenantID, JobTypeBuilderScript, JobStatusInactive, scriptID.String(), fnName).Scan(&existingID)
	if err == nil {
		_, err = pool.Exec(ctx, `
			UPDATE jobs
			SET status = $3, cron_expr = $4, timezone = $5,
			    next_run_at = $6, last_error = NULL,
			    description = $7, created_by = $8
			WHERE tenant_id = $1 AND id = $2
		`, tenantID, existingID, JobStatusActive, cronExpr, tz, nextRun, description, createdBy)
		if err != nil {
			return uuid.Nil, fmt.Errorf("reviving builder_script job: %w", err)
		}
		return existingID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("checking existing builder_script job: %w", err)
	}

	// Fresh insert. The partial unique index rejects a second active row
	// for the same (script_id, fn_name) — surfaced as a Postgres error
	// which the caller translates to "already scheduled".
	jobID := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO jobs (
			id, tenant_id, created_by, description, cron_expr, timezone,
			channel_id, job_type, status, next_run_at, config
		) VALUES ($1, $2, $3, $4, $5, $6, '', $7, $8, $9, $10::jsonb)
	`, jobID, tenantID, createdBy, description, cronExpr, tz,
		JobTypeBuilderScript, JobStatusActive, nextRun, configJSON)
	if err != nil {
		return uuid.Nil, fmt.Errorf("inserting builder_script job: %w", err)
	}
	return jobID, nil
}

// DeactivateBuilderScriptTask flips an active job_type='builder_script'
// row to status='inactive'. Row survives so history + cron expression are
// preserved and a later UpsertBuilderScriptTask can revive it.
// Returns true if a row was flipped.
func DeactivateBuilderScriptTask(ctx context.Context, pool *pgxpool.Pool, tenantID, scriptID uuid.UUID, fnName string) (bool, error) {
	tag, err := pool.Exec(ctx, `
		UPDATE jobs
		SET status = $4
		WHERE tenant_id = $1
		  AND job_type = $2
		  AND status = $3
		  AND config->>'script_id' = $5
		  AND config->>'fn_name'   = $6
	`, tenantID, JobTypeBuilderScript, JobStatusActive, JobStatusInactive, scriptID.String(), fnName)
	if err != nil {
		return false, fmt.Errorf("deactivating builder_script job: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// EnsureBuiltinTask creates a builtin job if it doesn't already exist for the tenant.
// Uses the description as a unique key per tenant (builtin jobs have fixed descriptions).
func EnsureBuiltinTask(ctx context.Context, pool *pgxpool.Pool, tenantID, createdBy uuid.UUID, description, cronExpr, tz string) error {
	var exists bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM jobs WHERE tenant_id = $1 AND job_type = $3 AND description = $2)
	`, tenantID, description, JobTypeBuiltin).Scan(&exists)
	if err != nil {
		return fmt.Errorf("checking builtin job: %w", err)
	}
	if exists {
		return nil
	}

	nextRun, err := NextCronRun(cronExpr, tz, time.Now())
	if err != nil {
		return fmt.Errorf("computing next run: %w", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO jobs (id, tenant_id, created_by, description, cron_expr, timezone, channel_id, job_type, next_run_at)
		VALUES ($1, $2, $3, $4, $5, $6, '', $8, $7)
	`, uuid.New(), tenantID, createdBy, description, cronExpr, tz, nextRun, JobTypeBuiltin)
	if err != nil {
		return fmt.Errorf("creating builtin job: %w", err)
	}
	return nil
}

// ClaimDueTasks atomically claims up to `limit` active jobs whose
// next_run_at has passed. Claim = flip status from 'active' to 'running'
// under SELECT FOR UPDATE SKIP LOCKED, so multiple scheduler instances
// (e.g. during a rolling deploy) never pick up the same job.
//
// After the returned jobs finish, the caller must set status to
// 'completed' (one-time) or back to 'active' with a new next_run_at
// (recurring) — see CompleteTask / UpdateJobAfterRun.
func ClaimDueTasks(ctx context.Context, pool *pgxpool.Pool, limit int) ([]Job, error) {
	rows, err := pool.Query(ctx, `
		WITH claimed AS (
			SELECT id FROM jobs
			WHERE status = $2 AND next_run_at <= now()
			ORDER BY next_run_at
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE jobs t
		SET status = $3
		FROM claimed c
		WHERE t.id = c.id
		RETURNING t.id, t.tenant_id, t.created_by, t.description, t.cron_expr, t.timezone, t.channel_id, t.run_once, t.job_type, t.status, t.next_run_at, t.last_run_at, t.last_error, t.config, t.resume_session_id, t.model, t.skill_id, t.created_at
	`, limit, JobStatusActive, JobStatusRunning)
	if err != nil {
		return nil, fmt.Errorf("claiming due jobs: %w", err)
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		var t Job
		if err := rows.Scan(&t.ID, &t.TenantID, &t.CreatedBy, &t.Description, &t.CronExpr,
			&t.Timezone, &t.ChannelID, &t.RunOnce, &t.JobType, &t.Status, &t.NextRunAt, &t.LastRunAt, &t.LastError, &t.Config, &t.ResumeSessionID, &t.Model, &t.SkillID, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning claimed job: %w", err)
		}
		jobs = append(jobs, t)
	}
	return jobs, rows.Err()
}

// ClaimDueTasksForTenant is a tenant-scoped claim variant used by tests
// so parallel-running fixtures don't steal each other's due rows.
// Production code should always call ClaimDueTasks.
func ClaimDueTasksForTenant(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, limit int) ([]Job, error) {
	rows, err := pool.Query(ctx, `
		WITH claimed AS (
			SELECT id FROM jobs
			WHERE tenant_id = $1 AND status = $3 AND next_run_at <= now()
			ORDER BY next_run_at
			FOR UPDATE SKIP LOCKED
			LIMIT $2
		)
		UPDATE jobs t
		SET status = $4
		FROM claimed c
		WHERE t.id = c.id AND t.tenant_id = $1
		RETURNING t.id, t.tenant_id, t.created_by, t.description, t.cron_expr, t.timezone, t.channel_id, t.run_once, t.job_type, t.status, t.next_run_at, t.last_run_at, t.last_error, t.config, t.resume_session_id, t.model, t.skill_id, t.created_at
	`, tenantID, limit, JobStatusActive, JobStatusRunning)
	if err != nil {
		return nil, fmt.Errorf("claiming due jobs for tenant: %w", err)
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		var t Job
		if err := rows.Scan(&t.ID, &t.TenantID, &t.CreatedBy, &t.Description, &t.CronExpr,
			&t.Timezone, &t.ChannelID, &t.RunOnce, &t.JobType, &t.Status, &t.NextRunAt, &t.LastRunAt, &t.LastError, &t.Config, &t.ResumeSessionID, &t.Model, &t.SkillID, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning claimed job: %w", err)
		}
		jobs = append(jobs, t)
	}
	return jobs, rows.Err()
}

// RecoverStuckTasks resets any job stuck in 'running' older than the
// cutoff back to 'active' so another scheduler can re-claim it. Runs at
// scheduler startup to handle crashes where a previous run didn't reach
// CompleteTask / UpdateJobAfterRun.
func RecoverStuckTasks(ctx context.Context, pool *pgxpool.Pool, olderThan time.Duration) (int64, error) {
	cmd, err := pool.Exec(ctx, `
		UPDATE jobs SET status = $1
		WHERE status = $2 AND (last_run_at IS NULL OR last_run_at < now() - $3::interval)
	`, JobStatusActive, JobStatusRunning, olderThan.String())
	if err != nil {
		return 0, fmt.Errorf("recovering stuck jobs: %w", err)
	}
	return cmd.RowsAffected(), nil
}

// CompleteTask marks a one-time job as completed after execution.
func CompleteTask(ctx context.Context, pool *pgxpool.Pool, tenantID, jobID uuid.UUID, lastError *string) error {
	_, err := pool.Exec(ctx, `
		UPDATE jobs SET last_run_at = now(), status = $3, last_error = $4
		WHERE tenant_id = $1 AND id = $2
	`, tenantID, jobID, JobStatusCompleted, lastError)
	if err != nil {
		return fmt.Errorf("completing job: %w", err)
	}
	return nil
}

// UpdateJobAfterRun updates last_run_at, next_run_at, and last_error
// after execution. Flips status back to 'active' so the next cron tick
// can re-claim this recurring job.
func UpdateJobAfterRun(ctx context.Context, pool *pgxpool.Pool, tenantID, jobID uuid.UUID, nextRun time.Time, lastError *string) error {
	_, err := pool.Exec(ctx, `
		UPDATE jobs SET status = $5, last_run_at = now(), next_run_at = $3, last_error = $4
		WHERE tenant_id = $1 AND id = $2
	`, tenantID, jobID, nextRun, lastError, JobStatusActive)
	if err != nil {
		return fmt.Errorf("updating job after run: %w", err)
	}
	return nil
}

// RequeueJobForResumeTx flips a job back to 'active' with next_run_at=now
// and marks the session to resume into on the next scheduler claim. Used
// by decision-resolution to wake a paused workflow. Runs inside the
// caller's transaction so the event append, job flip, and resume marker
// all land atomically.
func RequeueJobForResumeTx(ctx context.Context, tx pgx.Tx, tenantID, jobID, resumeSessionID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE jobs SET status = $3, next_run_at = now(), resume_session_id = $4
		WHERE tenant_id = $1 AND id = $2
	`, tenantID, jobID, JobStatusActive, resumeSessionID)
	if err != nil {
		return fmt.Errorf("requeuing job for resume: %w", err)
	}
	return nil
}

// ClearTaskResumeSession clears resume_session_id after the scheduler has
// consumed it. Called by the scheduler after successful claim so a
// subsequent cron tick doesn't accidentally resume into the same session.
func ClearTaskResumeSession(ctx context.Context, pool *pgxpool.Pool, tenantID, jobID uuid.UUID) error {
	_, err := pool.Exec(ctx, `
		UPDATE jobs SET resume_session_id = NULL
		WHERE tenant_id = $1 AND id = $2
	`, tenantID, jobID)
	if err != nil {
		return fmt.Errorf("clearing job resume session: %w", err)
	}
	return nil
}
