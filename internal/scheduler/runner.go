// Package scheduler: runner.go defines the TaskRunner abstraction — the
// pluggable dispatch point for each task_type. The scheduler's claim loop
// fetches a task from the tasks table under SKIP LOCKED, then looks up a
// TaskRunner by task_type and hands off. This lets new task_types (builder
// scripts today; anything else later) plug into the same claim + cron
// infrastructure without branching inside scheduler.go.
//
// Registration is a process-global map keyed by task_type. Production wires
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

// TaskRunner dispatches a claimed task. One implementation per task_type.
// Run is expected to handle its own audit/next_run_at bookkeeping because
// the bookkeeping differs per type (agent runs open a session, builtin
// runs call a native handler, builder_script runs write a script_runs row
// and re-check admin status). The scheduler only owns claim + dispatch.
type TaskRunner interface {
	TaskType() string
	Run(ctx context.Context, task *models.Task) error
}

// runnerRegistry guards the process-global runner map. Last registration
// wins; this is intentional so tests can swap runners in and out without
// process restart.
var (
	runnerMu   sync.RWMutex
	runnerByTT = map[string]TaskRunner{}
)

// RegisterTaskRunner installs (or replaces) the runner for a task_type.
// Idempotent; callers don't need to check whether one is already set.
// Nil r is a no-op — use ClearTaskRunnerForTest to remove a registration.
func RegisterTaskRunner(r TaskRunner) {
	if r == nil {
		return
	}
	runnerMu.Lock()
	defer runnerMu.Unlock()
	runnerByTT[r.TaskType()] = r
}

// ClearTaskRunnerForTest removes a task_type's runner registration.
// Exclusive to tests — production code never unregisters a runner.
func ClearTaskRunnerForTest(taskType string) {
	runnerMu.Lock()
	defer runnerMu.Unlock()
	delete(runnerByTT, taskType)
}

// runnerFor returns the runner for a given task_type, or nil if none is
// registered. The claim loop logs a warning and skips rows with no runner
// (rather than marking them in error) so a rolling deploy where the new
// binary starts before the builder app's Init runs doesn't permanently
// break scheduled rows.
func runnerFor(taskType string) TaskRunner {
	runnerMu.RLock()
	defer runnerMu.RUnlock()
	return runnerByTT[taskType]
}

// dispatchTask resolves the runner and invokes Run. Returns an error only
// when no runner is registered; Run-returned errors are logged by the
// runner itself (which knows the right audit context) and swallowed here.
func (s *Scheduler) dispatchTask(ctx context.Context, task *models.Task) {
	r := runnerFor(string(task.TaskType))
	if r == nil {
		slog.Warn("no task runner for task_type; skipping", "task_id", task.ID, "task_type", task.TaskType)
		// Best effort: flip back to active so the task is retried later
		// once the runner is registered. If this fails we log and the
		// RecoverStuckTasks sweep will eventually handle it.
		s.returnClaimedTaskToActive(ctx, *task)
		return
	}
	if err := r.Run(ctx, task); err != nil {
		slog.Error("task runner failed", "task_id", task.ID, "task_type", task.TaskType, "error", err)
	}
}

// returnClaimedTaskToActive un-claims a task (status=running → active)
// without advancing next_run_at. Used when dispatch can't find a runner —
// we want the task re-tried on the next tick once the runner is wired,
// not wedged in 'running' until RecoverStuckTasks's 15-minute sweep.
func (s *Scheduler) returnClaimedTaskToActive(ctx context.Context, task models.Task) {
	_, err := s.pool.Exec(ctx, `
		UPDATE tasks SET status = $3
		WHERE tenant_id = $1 AND id = $2
	`, task.TenantID, task.ID, models.TaskStatusActive)
	if err != nil {
		slog.Error("returning unclaimed task to active", "task_id", task.ID, "error", err)
	}
}
