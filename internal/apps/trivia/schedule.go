// Package trivia: schedule.go declares the app's recurring work.
//
// There is exactly one scheduled task, and it is the BACKSTOP half of the
// deadline sweep. The 500ms half is a process goroutine in sweeper.go, and
// the comment there explains why cron structurally cannot do that job: it is
// five-field, so one minute is its floor, while a countdown the room is
// watching needs about a second.
//
// What CAN live here lives here, exactly as CLAUDE.md requires: a per-tenant
// jobs row with run history, last_error and audit, so an operator can see
// that the safety net is running and when it last fired.
package trivia

import (
	"context"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/scheduler"
)

// registerScheduledTasks declares the minute-granularity sweep.
func (a *App) registerScheduledTasks() {
	scheduler.RegisterScheduledTask(scheduler.ScheduledTask{
		Key:         "trivia.sweep",
		Description: "Advance any trivia game whose phase clock has run out",
		DefaultCron: "* * * * *",
		AppliesTo: func(ctx context.Context, tenantID uuid.UUID) bool {
			return apps.IsEnabled(ctx, tenantID, AppName)
		},
		Run: func(ctx context.Context, job models.Job) error {
			if a.svc == nil {
				return nil
			}
			return a.svc.SweepTenant(ctx, job.TenantID)
		},
	})
}
