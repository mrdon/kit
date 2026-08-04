// Package models: job_runtime.go holds the scheduler-facing half of the jobs
// table — claiming due rows, recovering rows a crashed scheduler left behind,
// and the post-run bookkeeping. job.go owns creation, lookup, and listing;
// job_registry.go owns convergence of code-registered rows.
//
// The shared column list and row scanner live here because every query in all
// three files needs them, and the list was previously copy-pasted seven times.
package models

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// jobColumns is the canonical select/returning list for a jobs row, in the
// order scanJob expects. jobColumnsT is the same list under the `t` alias
// used by queries that join or update-with-returning.
const jobColumns = `id, tenant_id, created_by, description, cron_expr, timezone, ` +
	`channel_id, run_once, job_type, status, next_run_at, last_run_at, last_error, ` +
	`config, resume_session_id, model, skill_id, created_at, builtin_key, lane, claimed_at`

const jobColumnsT = `t.id, t.tenant_id, t.created_by, t.description, t.cron_expr, t.timezone, ` +
	`t.channel_id, t.run_once, t.job_type, t.status, t.next_run_at, t.last_run_at, t.last_error, ` +
	`t.config, t.resume_session_id, t.model, t.skill_id, t.created_at, t.builtin_key, t.lane, t.claimed_at`

// jobScanner is satisfied by both pgx.Row and pgx.Rows.
type jobScanner interface {
	Scan(dest ...any) error
}

// scanJob reads one row selected with jobColumns.
func scanJob(s jobScanner) (Job, error) {
	var t Job
	err := s.Scan(&t.ID, &t.TenantID, &t.CreatedBy, &t.Description, &t.CronExpr,
		&t.Timezone, &t.ChannelID, &t.RunOnce, &t.JobType, &t.Status, &t.NextRunAt,
		&t.LastRunAt, &t.LastError, &t.Config, &t.ResumeSessionID, &t.Model,
		&t.SkillID, &t.CreatedAt, &t.BuiltinKey, &t.Lane, &t.ClaimedAt)
	return t, err
}

// collectJobs drains a rows cursor selected with jobColumns. Closes rows.
func collectJobs(rows pgx.Rows) ([]Job, error) {
	defer rows.Close()
	var jobs []Job
	for rows.Next() {
		t, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning job: %w", err)
		}
		jobs = append(jobs, t)
	}
	return jobs, rows.Err()
}

// ClaimDueTasks atomically claims up to `limit` active jobs in one lane
// whose next_run_at has passed. Claim = flip status from 'active' to
// 'running' under SELECT FOR UPDATE SKIP LOCKED, so multiple scheduler
// instances (e.g. during a rolling deploy) never pick up the same job.
//
// The lane filter is what keeps a slow agent run from starving a due
// calendar sync: each lane claims from its own queue, so a full agent lane
// is invisible to the function lane.
//
// After the returned jobs finish, the caller must set status to
// 'completed' (one-time) or back to 'active' with a new next_run_at
// (recurring) — see CompleteTask / UpdateJobAfterRun.
func ClaimDueTasks(ctx context.Context, pool *pgxpool.Pool, lane JobLane, limit int) ([]Job, error) {
	rows, err := pool.Query(ctx, `
		WITH claimed AS (
			SELECT id FROM jobs
			WHERE lane = $4 AND status = $2 AND next_run_at <= now()
			ORDER BY next_run_at
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		)
		UPDATE jobs t
		SET status = $3, claimed_at = now()
		FROM claimed c
		WHERE t.id = c.id
		RETURNING `+jobColumnsT,
		limit, JobStatusActive, JobStatusRunning, lane)
	if err != nil {
		return nil, fmt.Errorf("claiming due jobs in lane %q: %w", lane, err)
	}
	return collectJobs(rows)
}

// ClaimDueTasksForTenant is a tenant-scoped claim variant used by tests
// so parallel-running fixtures don't steal each other's due rows.
// Production code should always call ClaimDueTasks.
func ClaimDueTasksForTenant(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, lane JobLane, limit int) ([]Job, error) {
	rows, err := pool.Query(ctx, `
		WITH claimed AS (
			SELECT id FROM jobs
			WHERE tenant_id = $1 AND lane = $5 AND status = $3 AND next_run_at <= now()
			ORDER BY next_run_at
			FOR UPDATE SKIP LOCKED
			LIMIT $2
		)
		UPDATE jobs t
		SET status = $4, claimed_at = now()
		FROM claimed c
		WHERE t.id = c.id AND t.tenant_id = $1
		RETURNING `+jobColumnsT,
		tenantID, limit, JobStatusActive, JobStatusRunning, lane)
	if err != nil {
		return nil, fmt.Errorf("claiming due jobs for tenant in lane %q: %w", lane, err)
	}
	return collectJobs(rows)
}

// RecoverStuckTasks resets any job stuck in 'running' older than the
// cutoff back to 'active' so another scheduler can re-claim it. Runs at
// scheduler startup to handle crashes where a previous run didn't reach
// CompleteTask / UpdateJobAfterRun.
//
// Age is COALESCE(claimed_at, last_run_at, created_at), not last_run_at
// alone. A row that has never completed has last_run_at NULL, so the old
// predicate treated every freshly-created registry row as infinitely stale
// and reset it the instant a sibling scheduler claimed it. created_at is
// NOT NULL, so the coalesce always has a floor.
func RecoverStuckTasks(ctx context.Context, pool *pgxpool.Pool, olderThan time.Duration) (int64, error) {
	cmd, err := pool.Exec(ctx, `
		UPDATE jobs SET status = $1, claimed_at = NULL
		WHERE status = $2
		  AND COALESCE(claimed_at, last_run_at, created_at) < now() - $3::interval
	`, JobStatusActive, JobStatusRunning, olderThan.String())
	if err != nil {
		return 0, fmt.Errorf("recovering stuck jobs: %w", err)
	}
	return cmd.RowsAffected(), nil
}

// CompleteTask marks a one-time job as completed after execution.
func CompleteTask(ctx context.Context, pool *pgxpool.Pool, tenantID, jobID uuid.UUID, lastError *string) error {
	_, err := pool.Exec(ctx, `
		UPDATE jobs SET last_run_at = now(), status = $3, last_error = $4, claimed_at = NULL
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
		UPDATE jobs SET status = $5, last_run_at = now(), next_run_at = $3, last_error = $4, claimed_at = NULL
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
		UPDATE jobs SET status = $3, next_run_at = now(), resume_session_id = $4, claimed_at = NULL
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
