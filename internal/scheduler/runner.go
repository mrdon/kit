// Package scheduler: runner.go defines the JobRunner abstraction — the
// pluggable dispatch point for each job_type. The scheduler's claim loop
// fetches a job from the jobs table under SKIP LOCKED, then looks up a
// JobRunner by job_type and hands off. This lets new job_types (builder
// scripts today; anything else later) plug into the same claim + cron
// infrastructure without branching inside scheduler.go.
//
// Registration is a process-global map keyed by job_type. Production wires
// the agent + builtin runners from scheduler.Start, and app code (builder)
// registers its own runner at Init time. Tests swap runners in and out via
// Register/ClearForTest.
package scheduler

import (
	"context"
	"log/slog"
	"sync"

	"github.com/mrdon/kit/internal/models"
)

// JobRunner dispatches a claimed job. One implementation per job_type.
// Run is expected to handle its own audit/next_run_at bookkeeping because
// the bookkeeping differs per type (agent runs open a session, builtin
// runs call a native handler, builder_script runs write a script_runs row
// and re-check admin status). The scheduler only owns claim + dispatch.
type JobRunner interface {
	JobType() string
	Run(ctx context.Context, job *models.Job) error
}

// runnerRegistry guards the process-global runner map. Last registration
// wins; this is intentional so tests can swap runners in and out without
// process restart.
var (
	runnerMu   sync.RWMutex
	runnerByTT = map[string]JobRunner{}
)

// RegisterJobRunner installs (or replaces) the runner for a job_type.
// Idempotent; callers don't need to check whether one is already set.
// Nil r is a no-op — use ClearJobRunnerForTest to remove a registration.
func RegisterJobRunner(r JobRunner) {
	if r == nil {
		return
	}
	runnerMu.Lock()
	defer runnerMu.Unlock()
	runnerByTT[r.JobType()] = r
}

// ClearJobRunnerForTest removes a job_type's runner registration.
// Exclusive to tests — production code never unregisters a runner.
func ClearJobRunnerForTest(jobType string) {
	runnerMu.Lock()
	defer runnerMu.Unlock()
	delete(runnerByTT, jobType)
}

// runnerFor returns the runner for a given job_type, or nil if none is
// registered. The claim loop logs a warning and skips rows with no runner
// (rather than marking them in error) so a rolling deploy where the new
// binary starts before the builder app's Init runs doesn't permanently
// break scheduled rows.
func runnerFor(jobType string) JobRunner {
	runnerMu.RLock()
	defer runnerMu.RUnlock()
	return runnerByTT[jobType]
}

// dispatchTask resolves the runner and invokes Run. Returns an error only
// when no runner is registered; Run-returned errors are logged by the
// runner itself (which knows the right audit context) and swallowed here.
func (s *Scheduler) dispatchTask(ctx context.Context, job *models.Job) {
	r := runnerFor(string(job.JobType))
	if r == nil {
		slog.Warn("no job runner for job_type; skipping", "job_id", job.ID, "job_type", job.JobType)
		// Best effort: flip back to active so the job is retried later
		// once the runner is registered. If this fails we log and the
		// RecoverStuckTasks sweep will eventually handle it.
		s.returnClaimedTaskToActive(ctx, *job)
		return
	}
	if err := r.Run(ctx, job); err != nil {
		slog.Error("job runner failed", "job_id", job.ID, "job_type", job.JobType, "error", err)
	}
}

// returnClaimedTaskToActive un-claims a job (status=running → active)
// without advancing next_run_at. Used when dispatch can't find a runner —
// we want the job re-tried on the next tick once the runner is wired,
// not wedged in 'running' until RecoverStuckTasks's 15-minute sweep.
func (s *Scheduler) returnClaimedTaskToActive(ctx context.Context, job models.Job) {
	_, err := s.pool.Exec(ctx, `
		UPDATE jobs SET status = $3
		WHERE tenant_id = $1 AND id = $2
	`, job.TenantID, job.ID, models.JobStatusActive)
	if err != nil {
		slog.Error("returning unclaimed job to active", "job_id", job.ID, "error", err)
	}
}
