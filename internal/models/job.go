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

	"github.com/mrdon/kit/internal/anthropic"
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

// JobLane is the execution pool a job is claimed into. Deliberately not
// derived from JobType: some builtin registrations call the LLM (the task
// app's email intake runs a full agent loop; a native sweep calls
// the Messages API directly) and must stay serialized alongside agent runs,
// while the remaining builtins are IO-bound and safe to run wide.
type JobLane string

const (
	// JobLaneAgent is serialized — everything in it shares the Anthropic
	// org rate limit.
	JobLaneAgent JobLane = "agent"
	// JobLaneFunction is native work: syncs, reconciles, sweeps, and
	// builder scripts. Bounded by Postgres and outbound HTTP, not tokens.
	JobLaneFunction JobLane = "function"
)

// Tier names persisted in jobs.model. Picked at create_task time by a
// Haiku classifier pass; the scheduler resolves the tier to an Anthropic
// model ID via JobModelID before calling agent.Run. Kept as short tier
// names (not full Anthropic model IDs) so a future pricing/ID shift only
// updates JobModelID, not every DB row.
const (
	JobModelHaiku  = "haiku"
	JobModelSonnet = "sonnet"
)

// JobModelID maps a job's persisted tier name to the Anthropic Messages
// API model ID. Empty / unknown values fall back to the Haiku ID so a
// row with no tier set still runs.
func JobModelID(tier string) string {
	switch tier {
	case JobModelSonnet:
		return anthropic.ModelSonnet
	default:
		return anthropic.ModelHaiku
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
	// BuiltinKey is the stable identity of a code-registered job (e.g.
	// "events.reconcile"). Nil for user-created agent jobs and for
	// builder_script rows. Handler lookup keys on this, never on
	// Description — a description is a label and may be edited or
	// renamed in code without orphaning the row.
	BuiltinKey *string
	// Lane is the execution pool this row is claimed into.
	Lane JobLane
	// ClaimedAt is stamped when the row flips to 'running' and cleared on
	// completion. Drives stuck-row recovery; nil when not running.
	ClaimedAt *time.Time
}

// IsSystem reports whether this row was created by the code registry rather
// than by a user. System rows carry no job_scopes row, which is what keeps
// them out of scoped listings.
func (j Job) IsSystem() bool { return j.BuiltinKey != nil && *j.BuiltinKey != "" }

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

	jobID := uuid.New()
	row := tx.QueryRow(ctx, `
		INSERT INTO jobs (id, tenant_id, created_by, description, cron_expr, timezone, channel_id, run_once, next_run_at, model, skill_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING `+jobColumns,
		jobID, tenantID, createdBy, description, cronExpr, tz, channelID, runOnce, nextRun, model, skillID)
	created, err := scanJob(row)
	if err != nil {
		return nil, fmt.Errorf("creating job: %w", err)
	}
	job := &created

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
	row := q.QueryRow(ctx, `
		SELECT `+jobColumns+`
		FROM jobs WHERE tenant_id = $1 AND id = $2
	`, tenantID, jobID)
	t, err := scanJob(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // not found is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("getting job: %w", err)
	}
	return &t, nil
}

// ListJobsForContext returns jobs visible to the user via scope filtering.
func ListJobsForContext(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID, roleIDs []uuid.UUID) ([]Job, error) {
	scopeSQL, scopeArgs := ScopeFilterIDs("sc", 2, userID, roleIDs)
	args := append([]any{tenantID}, scopeArgs...)
	rows, err := pool.Query(ctx, `
		SELECT DISTINCT `+jobColumnsT+`
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
	return collectJobs(rows)
}

// ListAllJobs returns every job in the tenant, ignoring scope. This is the
// admin-superuser path: callers must have already established the caller is
// an admin (the service layer owns that check). Mirrors ListJobsForContext
// minus the job_scopes/scopes join.
func ListAllJobs(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) ([]Job, error) {
	rows, err := pool.Query(ctx, `
		SELECT `+jobColumns+`
		FROM jobs
		WHERE tenant_id = $1
		ORDER BY created_at
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("listing all jobs: %w", err)
	}
	return collectJobs(rows)
}

// JobScope is one scope row for a job — an alias of the shared ScopeLabel so
// every scoped entity denormalizes scope the same way.
type JobScope = ScopeLabel

// SetJobScope replaces a job's scope rows with a single scope: a role
// (roleID set), a user (userID set), or tenant-wide (both nil). Atomic swap.
// Authorization (who may scope a job where) is the service layer's job.
func SetJobScope(ctx context.Context, pool *pgxpool.Pool, tenantID, jobID uuid.UUID, roleID, userID *uuid.UUID) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`DELETE FROM job_scopes WHERE tenant_id = $1 AND job_id = $2`,
		tenantID, jobID); err != nil {
		return fmt.Errorf("clearing job scopes: %w", err)
	}
	scopeID, err := GetOrCreateScopeTx(ctx, tx, tenantID, roleID, userID)
	if err != nil {
		return fmt.Errorf("get-or-create scope: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO job_scopes (tenant_id, job_id, scope_id)
		VALUES ($1, $2, $3) ON CONFLICT DO NOTHING
	`, tenantID, jobID, scopeID); err != nil {
		return fmt.Errorf("setting job scope: %w", err)
	}
	return tx.Commit(ctx)
}

// ListJobScopesBatch loads the scope rows for many jobs, via the shared
// scope-label loader. A job normally has exactly one scope row; tenant-wide
// jobs have none (the absence is what makes them tenant-wide).
func ListJobScopesBatch(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, jobIDs []uuid.UUID) (map[uuid.UUID][]JobScope, error) {
	return LoadScopeLabels(ctx, pool, tenantID, "job_scopes", "job_id", jobIDs)
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
// ListSkillIDsReferencedByJobs returns the distinct skill IDs that any
// active or inactive job in this tenant points at via Jobs.SkillID.
// Used by the website chat widget to exclude tenant-internal workflow
// skills from the public Q&A surface — if a skill is wired into a
// scheduled job, it's almost certainly not customer-facing FAQ
// material.
func ListSkillIDsReferencedByJobs(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := pool.Query(ctx, `
		SELECT DISTINCT skill_id
		FROM jobs
		WHERE tenant_id = $1 AND skill_id IS NOT NULL
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("listing job-referenced skill ids: %w", err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning job-referenced skill id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

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
			    description = $7, created_by = $8, lane = $9
			WHERE tenant_id = $1 AND id = $2
		`, tenantID, existingID, JobStatusActive, cronExpr, tz, nextRun, description, createdBy, JobLaneFunction)
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
	// lane is set explicitly: the column defaults to 'agent' (correct for
	// user-created jobs), but a builder script is native work and belongs
	// in the function lane even though it can call the LLM from inside.
	jobID := uuid.New()
	_, err = pool.Exec(ctx, `
		INSERT INTO jobs (
			id, tenant_id, created_by, description, cron_expr, timezone,
			channel_id, job_type, status, next_run_at, config, lane
		) VALUES ($1, $2, $3, $4, $5, $6, '', $7, $8, $9, $10::jsonb, $11)
	`, jobID, tenantID, createdBy, description, cronExpr, tz,
		JobTypeBuilderScript, JobStatusActive, nextRun, configJSON, JobLaneFunction)
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
