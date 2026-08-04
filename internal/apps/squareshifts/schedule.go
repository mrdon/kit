// Package squareshifts: schedule.go declares the app's recurring work.
//
// Previously goroutine tickers (apps.CronJob) that fanned out across tenants
// internally, leaving no row, no last run, and no audit trail. The 12-hour
// reconcile in particular was rebuilt on every process start, so on a service
// that deploys more than twice a day it effectively never fired.
package squareshifts

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/googlecalendar"
	"github.com/mrdon/kit/internal/apps/square"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/scheduler"
)

// registerScheduledTasks declares the sync and reconcile passes. Minute
// fields are offset from the other apps' syncs so tenants don't stack their
// outbound Square and Google calls on the same tick.
func (a *App) registerScheduledTasks() {
	scheduler.RegisterScheduledTask(scheduler.ScheduledTask{
		Key:         "squareshifts.sync",
		Description: "Sync Square shifts to Google Calendar",
		DefaultCron: "8,23,38,53 * * * *",
		AppliesTo:   a.squareConnected,
		Run: func(ctx context.Context, job models.Job) error {
			_, err := a.RunSync(ctx, job.TenantID, "schedule")
			return ignoreUnconfigured(err)
		},
	})

	scheduler.RegisterScheduledTask(scheduler.ScheduledTask{
		Key:         "squareshifts.reconcile",
		Description: "Reconcile Square shifts with Google Calendar",
		DefaultCron: "41 5,17 * * *",
		AppliesTo:   a.squareConnected,
		Run: func(ctx context.Context, job models.Job) error {
			_, err := a.RunReconcile(ctx, job.TenantID)
			return ignoreUnconfigured(err)
		},
	})
}

// squareConnected reports whether this tenant has the app enabled and a
// Square integration to pull shifts from.
func (a *App) squareConnected(ctx context.Context, tenantID uuid.UUID) bool {
	if a.pool == nil || !apps.IsEnabled(ctx, tenantID, AppName) {
		return false
	}
	var exists bool
	err := a.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM integrations
			WHERE tenant_id = $1 AND provider = 'square'
		)`, tenantID).Scan(&exists)
	if err != nil {
		return false
	}
	return exists
}

// ignoreUnconfigured swallows the "nothing wired up here" errors so they
// don't land in last_error. AppliesTo filters most of these out, but either
// side can be disconnected between the reconcile pass and the run.
func ignoreUnconfigured(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, googlecalendar.ErrNotConfigured) || errors.Is(err, square.ErrNotConfigured) {
		return nil
	}
	return fmt.Errorf("square shifts schedule: %w", err)
}
