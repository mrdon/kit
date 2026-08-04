// Package events: schedule.go declares the app's recurring work.
//
// These used to be goroutine tickers (apps.CronJob) that fanned out across
// tenants internally. That left no trace — no row, no last run, no audit —
// and the ticker was rebuilt on every process start, so a 12-hour reconcile
// only fired if the container happened to live 12 hours. On a service that
// deploys more than twice a day, it effectively never ran.
package events

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/googlecalendar"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/scheduler"
)

// registerScheduledTasks declares the sync and reconcile passes.
//
// Minute fields are offset from the other apps' syncs so tenants don't all
// stack their outbound Google calls on the same tick.
func (a *App) registerScheduledTasks() {
	scheduler.RegisterScheduledTask(scheduler.ScheduledTask{
		Key:         "events.sync",
		Description: "Sync events to Google Calendar",
		DefaultCron: "3,18,33,48 * * * *",
		AppliesTo:   a.calendarConfigured,
		Run: func(ctx context.Context, job models.Job) error {
			_, err := a.RunSync(ctx, job.TenantID, "schedule")
			return ignoreUnconfigured(err)
		},
	})

	// The sync trusts its stored content hash and skips unchanged events,
	// which makes it cheap but blind to edits made directly in Google. This
	// pass compares against the calendar's actual state to heal those, so it
	// is deliberately much less frequent.
	scheduler.RegisterScheduledTask(scheduler.ScheduledTask{
		Key:         "events.reconcile",
		Description: "Reconcile events with Google Calendar",
		DefaultCron: "17 4,16 * * *",
		AppliesTo:   a.calendarConfigured,
		Run: func(ctx context.Context, job models.Job) error {
			_, err := a.RunReconcile(ctx, job.TenantID)
			return ignoreUnconfigured(err)
		},
	})
}

// calendarConfigured reports whether this tenant has events enabled and a
// calendar to sync to. Without it every tenant would carry event rows that
// could only ever fail.
func (a *App) calendarConfigured(ctx context.Context, tenantID uuid.UUID) bool {
	if a.pool == nil || !apps.IsEnabled(ctx, tenantID, AppName) {
		return false
	}
	return hasCalendar(ctx, a.pool, tenantID)
}

func hasCalendar(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) bool {
	var exists bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM app_event_settings
			WHERE tenant_id = $1 AND calendar_id <> ''
		)`, tenantID).Scan(&exists)
	if err != nil {
		return false
	}
	return exists
}

// ignoreUnconfigured swallows the two "nothing to do here" errors so they
// don't land in last_error. AppliesTo already filters most of these out, but
// a calendar can be disconnected between the reconcile pass and the run.
func ignoreUnconfigured(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrNoCalendar) || errors.Is(err, googlecalendar.ErrNotConfigured) {
		return nil
	}
	return fmt.Errorf("events schedule: %w", err)
}
