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
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/googlecalendar"
	"github.com/mrdon/kit/internal/apps/square"
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
	// pass compares against the calendar's actual state to heal those.
	//
	// Once nightly. It costs an extra list call per tenant, and drift here
	// is someone editing the calendar by hand — rare, and not urgent enough
	// to chase more often than that.
	scheduler.RegisterScheduledTask(scheduler.ScheduledTask{
		Key:         "events.reconcile",
		Description: "Reconcile events with Google Calendar",
		DefaultCron: "17 4 * * *",
		AppliesTo:   a.calendarConfigured,
		Run: func(ctx context.Context, job models.Job) error {
			_, err := a.RunReconcile(ctx, job.TenantID)
			return ignoreUnconfigured(err)
		},
	})

	// The website is a static build, so Kit and the web are only in step
	// just after one. A nightly build closes that gap without anyone having
	// to remember to press publish — an event edited on Tuesday afternoon is
	// live by Wednesday morning rather than whenever someone next notices.
	//
	// 2am tenant-local, clear of the 3am profile sync.
	scheduler.RegisterScheduledTask(scheduler.ScheduledTask{
		Key:         "events.publish_site",
		Description: "Publish the events website overnight",
		DefaultCron: "0 2 * * *",
		AppliesTo:   a.buildHookConfigured,
		Run:         a.publishSiteIfChanged,
	})

	// Morning-of, in the venue's own zone. The notice has to land before the
	// first person starts setting up -- told at 4pm that the room needs
	// reserving for 30 at 7, they have already lost the afternoon -- and the
	// opener is usually on by late morning, so 8am clears the whole day.
	//
	// Once daily rather than hourly: the notice is a briefing, and a second
	// one at 9am saying the same thing trains people to ignore the first.
	// A day whose plan genuinely changes re-sends on the next run because the
	// notice's content hash moves; an unchanged day stays quiet.
	scheduler.RegisterScheduledTask(scheduler.ScheduledTask{
		Key:         "events.shift_notices",
		Description: "DM today's events to the staff working",
		DefaultCron: "0 8 * * *",
		AppliesTo:   a.shiftNoticesConfigured,
		Run: func(ctx context.Context, job models.Job) error {
			_, err := a.RunShiftNotices(ctx, job.TenantID, "schedule")
			return ignoreUnconfigured(err)
		},
	})
}

// shiftNoticesConfigured reports whether this tenant can send notices at all:
// the app on, and Square connected to say who is working. Without the second,
// every tenant with events would carry a daily row that could only ever fail.
func (a *App) shiftNoticesConfigured(ctx context.Context, tenantID uuid.UUID) bool {
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

// publishSiteIfChanged rebuilds the website only when something is waiting
// to go out.
//
// Netlify bills build minutes, and most nights nothing has changed — a blind
// nightly build would spend them to produce a byte-identical site. Checking
// first costs one query.
func (a *App) publishSiteIfChanged(ctx context.Context, job models.Job) error {
	status, err := a.svc.SiteStatus(ctx, job.TenantID)
	if err != nil {
		return fmt.Errorf("checking site status: %w", err)
	}
	if len(status.Pending) == 0 {
		return nil
	}
	if _, err := a.svc.PublishSite(ctx, job.TenantID, "schedule"); err != nil {
		return fmt.Errorf("publishing site: %w", err)
	}
	return nil
}

// buildHookConfigured reports whether this tenant has somewhere to publish
// to. Without it, every events tenant would carry a nightly row that could
// only ever fail.
func (a *App) buildHookConfigured(ctx context.Context, tenantID uuid.UUID) bool {
	if a.pool == nil || !apps.IsEnabled(ctx, tenantID, AppName) {
		return false
	}
	settings, err := getSettings(ctx, a.pool, tenantID)
	if err != nil {
		return false
	}
	return strings.TrimSpace(settings.SiteBuildHookURL) != ""
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
	if errors.Is(err, ErrNoCalendar) || errors.Is(err, googlecalendar.ErrNotConfigured) ||
		errors.Is(err, square.ErrNotConfigured) {
		return nil
	}
	return fmt.Errorf("events schedule: %w", err)
}
