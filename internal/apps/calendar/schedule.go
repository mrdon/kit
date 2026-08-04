// Package calendar: schedule.go declares the app's recurring work.
//
// The sweep used to be a goroutine ticker fanning out over every calendar in
// the database. Per-tenant rows mean a tenant whose feed URL has gone bad
// carries that error on its own job row instead of it living only in the
// process logs.
package calendar

import (
	"context"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/scheduler"
)

// registerScheduledTasks declares the calendar refresh. The minute field is
// offset from the other apps' syncs so outbound fetches don't all stack on
// the same tick.
func (a *CalendarApp) registerScheduledTasks() {
	scheduler.RegisterScheduledTask(scheduler.ScheduledTask{
		Key:         "calendar.sync",
		Description: "Refresh subscribed calendars",
		DefaultCron: "11,26,41,56 * * * *",
		AppliesTo:   a.hasCalendars,
		Run: func(ctx context.Context, job models.Job) error {
			return a.svc.SyncTenantCalendars(ctx, job.TenantID)
		},
	})
}

// hasCalendars reports whether this tenant has the app enabled and anything
// to refresh.
func (a *CalendarApp) hasCalendars(ctx context.Context, tenantID uuid.UUID) bool {
	if a.svc == nil || a.svc.pool == nil || !apps.IsEnabled(ctx, tenantID, AppName) {
		return false
	}
	ok, err := listTenantsWithCalendars(ctx, a.svc.pool, tenantID)
	return err == nil && ok
}
