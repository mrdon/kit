// Package models: job_registry.go holds the queries that converge
// code-registered jobs. The scheduler's registry is desired state, the rows
// here are observed state, and RegistryUpsert / RegistryRetire are the two
// operations that move one toward the other.
//
// Identity is builtin_key, never description. The previous builtin mechanism
// matched handlers on the description string, so renaming a builtin orphaned
// every existing row and inserted a duplicate alongside it.
package models

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// registryStartupGrace is how far out a newly-created registry row's first
// run is pushed.
//
// During a rolling deploy the outgoing container is still running the old
// per-app goroutine tickers while the incoming one creates and claims these
// rows, so a job seeded to fire immediately can run twice against the same
// external calendar. Advancing to the first occurrence at least this far out
// costs a frequent job one skipped cycle and costs a daily job nothing.
const registryStartupGrace = 5 * time.Minute

// RegistryTaskRow is the desired state for one (tenant, key) pair.
type RegistryTaskRow struct {
	TenantID    uuid.UUID
	CreatedBy   uuid.UUID
	Key         string
	Description string
	CronExpr    string
	Timezone    string
	Lane        JobLane
}

// FirstRegistryRun returns the first cron occurrence at least
// registryStartupGrace from now. See the constant for why.
func FirstRegistryRun(cronExpr, tz string, now time.Time) (time.Time, error) {
	cutoff := now.Add(registryStartupGrace)
	next, err := NextCronRun(cronExpr, tz, now)
	if err != nil {
		return time.Time{}, err
	}
	// Bounded: a valid cron fires at least once a year, and each step moves
	// strictly forward, so this converges well inside the iteration cap.
	for i := 0; i < 366 && next.Before(cutoff); i++ {
		next, err = NextCronRun(cronExpr, tz, next)
		if err != nil {
			return time.Time{}, err
		}
	}
	return next, nil
}

// RegistryUpsert creates or converges one registry row.
//
// Code owns description, cron_expr, and lane — they are overwritten on every
// pass, so renaming a task in code updates its label everywhere. next_run_at
// is deliberately left alone unless the row is being revived or its schedule
// actually changed, so a rename doesn't push the job's next run out.
func RegistryUpsert(ctx context.Context, pool *pgxpool.Pool, row RegistryTaskRow) (*Job, error) {
	firstRun, err := FirstRegistryRun(row.CronExpr, row.Timezone, time.Now())
	if err != nil {
		return nil, fmt.Errorf("computing first run for %q: %w", row.Key, err)
	}

	q := pool.QueryRow(ctx, `
		INSERT INTO jobs (
			id, tenant_id, created_by, description, cron_expr, timezone,
			channel_id, job_type, lane, status, next_run_at, builtin_key
		) VALUES ($1, $2, $3, $4, $5, $6, '', $7, $8, $9, $10, $11)
		ON CONFLICT (tenant_id, builtin_key) WHERE builtin_key IS NOT NULL
		DO UPDATE SET
			description = EXCLUDED.description,
			cron_expr   = EXCLUDED.cron_expr,
			lane        = EXCLUDED.lane,
			job_type    = EXCLUDED.job_type,
			-- jobs.created_by is NOT NULL REFERENCES users ON DELETE CASCADE,
			-- so a system row is only as durable as the user it happens to
			-- name. Re-pointing at the current admin on every pass keeps that
			-- window down to one reconcile cycle. Deleting that admin still
			-- cascades the row away; it returns on the next pass with its
			-- run history reset. Fixing that properly means making
			-- created_by nullable, which is a wider change than this.
			created_by  = EXCLUDED.created_by,
			status      = CASE WHEN jobs.status = $12 THEN $9 ELSE jobs.status END,
			last_error  = CASE WHEN jobs.status = $12 THEN NULL ELSE jobs.last_error END,
			next_run_at = CASE
				WHEN jobs.status = $12 THEN EXCLUDED.next_run_at
				WHEN jobs.cron_expr IS DISTINCT FROM EXCLUDED.cron_expr THEN EXCLUDED.next_run_at
				ELSE jobs.next_run_at
			END
		RETURNING `+jobColumns,
		uuid.New(), row.TenantID, row.CreatedBy, row.Description, row.CronExpr,
		row.Timezone, JobTypeBuiltin, row.Lane, JobStatusActive, firstRun, row.Key,
		JobStatusInactive)

	job, err := scanJob(q)
	if err != nil {
		return nil, fmt.Errorf("upserting registry job %q: %w", row.Key, err)
	}
	return &job, nil
}

// RegistryRetire parks a row that should no longer run — its app was
// disabled, its precondition stopped holding, or its registration was
// deleted from the code entirely. The row survives so last_run_at and the
// audit trail outlive the reason it stopped; RegistryUpsert revives it if
// the task comes back.
//
// A running row is left alone: retiring mid-flight would strand it, and the
// next pass catches it once it settles.
func RegistryRetire(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, key, reason string) error {
	_, err := pool.Exec(ctx, `
		UPDATE jobs SET status = $3, last_error = $4
		WHERE tenant_id = $1 AND builtin_key = $2 AND status = $5
	`, tenantID, key, JobStatusInactive, reason, JobStatusActive)
	if err != nil {
		return fmt.Errorf("retiring registry job %q: %w", key, err)
	}
	return nil
}

// ListRegistryJobs returns every code-registered row for a tenant, active or
// not. The reconciler diffs this against the code registry to find rows whose
// registration has been deleted.
func ListRegistryJobs(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) ([]Job, error) {
	rows, err := pool.Query(ctx, `
		SELECT `+jobColumns+`
		FROM jobs
		WHERE tenant_id = $1 AND builtin_key IS NOT NULL
		ORDER BY builtin_key
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("listing registry jobs: %w", err)
	}
	return collectJobs(rows)
}
