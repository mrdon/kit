// Package scheduler: builtin.go holds the scheduler's own registered work and
// the execution path for code-registered rows.
//
// This used to be a hardcoded slice of tasks materialised for every tenant,
// with handlers looked up by description string — so renaming one orphaned
// its rows and inserted duplicates alongside them. Registrations now live in
// registry.go and bind by key; this file just declares the scheduler's own
// and runs whatever the registry resolves.
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/mrdon/kit/internal/models"
)

// registerSystemTasks declares the scheduler's own scheduled work. Called
// from New, so it lands before the first reconcile pass in Start.
func (s *Scheduler) registerSystemTasks() {
	RegisterScheduledTask(ScheduledTask{
		Key:         "system.profile_sync",
		Description: "Sync user profiles from Slack",
		DefaultCron: "0 3 * * *",
		Run: func(ctx context.Context, job models.Job) error {
			tenant, err := models.GetTenantByID(ctx, s.pool, job.TenantID)
			if err != nil {
				return fmt.Errorf("looking up tenant: %w", err)
			}
			if tenant == nil {
				return errors.New("tenant not found")
			}
			s.syncTenantProfiles(ctx, *tenant)
			return nil
		},
	})
}

// ExecuteBuiltinTask runs a registered task's handler and records the
// outcome. Exported so the MCP run_job tool can trigger one directly.
//
// Unlike the mechanism it replaces, a handler error is persisted to
// last_error rather than only logged — "did last night's sync fail" is
// answerable from the row.
func (s *Scheduler) ExecuteBuiltinTask(ctx context.Context, job models.Job) {
	var lastErr *string
	record := func(err error) {
		slog.Error("registered job failed", "job_id", job.ID, "key", job.BuiltinKey, "error", err)
		msg := err.Error()
		lastErr = &msg
	}

	switch {
	case job.BuiltinKey == nil || *job.BuiltinKey == "":
		record(errors.New("job has no builtin_key"))
	default:
		task, ok := scheduledTask(*job.BuiltinKey)
		if !ok {
			// The registration was removed while this row was in flight.
			// Return it to active with the reason recorded; the next
			// reconcile pass retires it. Deliberately not left 'running'
			// — that would wedge the row until the recovery sweep.
			record(errors.New(retiredUnregistered))
		} else if err := task.Run(ctx, job); err != nil {
			record(err)
		}
	}

	nextRun, err := models.NextCronRun(job.CronExpr, job.Timezone, time.Now())
	if err != nil {
		slog.Error("computing next run for registered job", "job_id", job.ID, "error", err)
		return
	}
	if err := models.UpdateJobAfterRun(ctx, s.pool, job.TenantID, job.ID, nextRun, lastErr); err != nil {
		slog.Error("updating registered job after run", "job_id", job.ID, "error", err)
	}
}
