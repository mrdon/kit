package squareshifts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/googlecalendar"
	"github.com/mrdon/kit/internal/apps/square"
)

// reconcileInterval is how often the drift-repair sweep runs. The regular
// 15-minute sync trusts its mapping + content hash and so can't see events
// deleted or orphaned directly in Google; this slower pass reconciles
// against the calendar's actual state to make Square authoritative. It's
// cheap (one extra Google list call) so the interval is conservative.
const reconcileInterval = 12 * time.Hour

// reconcilePlan is what a sweep would change: events to (re)create, and owned
// events to delete. Building it is read-only, so it doubles as the dry run.
// The Google handles are carried along so applying doesn't reload them.
type reconcilePlan struct {
	Create []desiredShift
	Delete []googlecalendar.Event

	gcal        *googlecalendar.Client
	calendarID  string
	windowStart time.Time
}

func (p reconcilePlan) empty() bool { return len(p.Create) == 0 && len(p.Delete) == 0 }

// PreviewReconcile computes what a reconcile would do without touching the
// calendar or writing an audit row. Use it before RunReconcile on a live
// calendar — it's the only way to see which events a sweep would delete.
func (a *App) PreviewReconcile(ctx context.Context, tenantID uuid.UUID) (reconcilePlan, error) {
	return a.planReconcile(ctx, tenantID)
}

// RunReconcile runs a reconciliation sweep for one tenant and records the
// outcome to audit_events with triggered_by "reconcile".
func (a *App) RunReconcile(ctx context.Context, tenantID uuid.UUID) (SyncSummary, error) {
	started := time.Now()
	sum, err := a.reconcileTenant(ctx, tenantID)
	if err != nil {
		a.auditFailed(ctx, tenantID, "reconcile", err, time.Since(started))
		return sum, err
	}
	// Only record a reconcile that actually repaired drift; a clean pass
	// (the normal case) leaves no audit noise.
	if sum.changed() {
		a.auditCompleted(ctx, tenantID, "reconcile", sum, time.Since(started))
	}
	return sum, nil
}

// reconcileTenant compares the calendar's actual Kit-authored events against
// the currently-published Square schedule and repairs drift: it recreates
// events that should exist but are missing (e.g. deleted in Google) and
// deletes in-window events that no longer back a published shift (orphans).
// Unlike the regular sync it consults Google's real state rather than the
// mapping table, so it heals out-of-band deletions.
func (a *App) reconcileTenant(ctx context.Context, tenantID uuid.UUID) (SyncSummary, error) {
	plan, err := a.planReconcile(ctx, tenantID)
	if err != nil {
		return SyncSummary{}, err
	}
	return a.applyReconcile(ctx, tenantID, plan)
}

// planReconcile computes the drift between Square and the calendar without
// changing anything. Read-only by construction: it issues no writes, so a
// caller can show the plan to an operator before applying it.
func (a *App) planReconcile(ctx context.Context, tenantID uuid.UUID) (reconcilePlan, error) {
	var plan reconcilePlan

	start, end := syncWindow()
	plan.windowStart = start

	shifts, err := square.Instance().ListPublishedShifts(ctx, tenantID, start, end)
	if err != nil {
		return plan, fmt.Errorf("pulling square shifts: %w", err)
	}
	plan.gcal, plan.calendarID, err = googlecalendar.Instance().LoadClient(ctx, tenantID)
	if err != nil {
		return plan, fmt.Errorf("loading google calendar: %w", err)
	}

	// desired: event id → the shift + event we want on the calendar.
	desired := make(map[string]desiredShift, len(shifts))
	for _, s := range shifts {
		if s.ShiftID == "" {
			continue
		}
		ev := buildEvent(s, tenantID)
		desired[ev.ID] = desiredShift{shift: s, event: ev}
	}

	// actual: every event on the calendar this app authored for this tenant.
	// Filtering on the full ownership stamp (not just the tenant) is what
	// keeps the orphan sweep below off other features' events on a shared
	// calendar — and off everything a human put there.
	actual, err := plan.gcal.ListEventsByPrivateProperties(ctx, plan.calendarID, googlecalendar.OwnerProps(AppName, tenantID))
	if err != nil {
		return plan, fmt.Errorf("listing calendar events: %w", err)
	}
	actualByID := make(map[string]googlecalendar.Event, len(actual))
	for _, e := range actual {
		actualByID[e.ID] = e
	}

	// Recreate desired events missing from the calendar (healed deletions).
	for id, d := range desired {
		if _, present := actualByID[id]; !present {
			plan.Create = append(plan.Create, d)
		}
	}

	// Delete stale events: ones we own (the list above is already filtered to
	// those) that no longer back a published shift. Past events (start <
	// window) are history and left untouched, and events beyond the window
	// are spared too — we didn't ask Square about that range, so their
	// absence from `desired` says nothing about whether the shift still
	// exists.
	for id, e := range actualByID {
		if _, want := desired[id]; want {
			continue
		}
		if !eventStartsInWindow(e, start, end) {
			continue
		}
		plan.Delete = append(plan.Delete, e)
	}
	return plan, nil
}

// applyReconcile executes a plan against Google Calendar and the mapping
// table. Every deletion here has already passed both the ownership and
// staleness tests in planReconcile.
func (a *App) applyReconcile(ctx context.Context, tenantID uuid.UUID, plan reconcilePlan) (SyncSummary, error) {
	var sum SyncSummary
	for _, d := range plan.Create {
		if _, err := plan.gcal.UpsertEvent(ctx, plan.calendarID, d.event); err != nil {
			return sum, fmt.Errorf("recreating event for shift %s: %w", d.shift.ShiftID, err)
		}
		startAt, perr := time.Parse(time.RFC3339, d.shift.StartAt)
		if perr != nil {
			startAt = plan.windowStart
		}
		if err := upsertMapping(ctx, a.pool, tenantID, d.shift.ShiftID, d.event.ID, startAt, 0, contentHash(d.event)); err != nil {
			return sum, err
		}
		sum.Created++
	}
	for _, e := range plan.Delete {
		if err := plan.gcal.DeleteEvent(ctx, plan.calendarID, e.ID); err != nil {
			return sum, fmt.Errorf("deleting orphan event %s: %w", e.ID, err)
		}
		if sid := privateProp(e, "squareShiftId"); sid != "" {
			_ = deleteMapping(ctx, a.pool, tenantID, sid)
		}
		sum.Deleted++
	}
	return sum, nil
}

type desiredShift struct {
	shift square.EnrichedShift
	event *googlecalendar.Event
}

// ReconcileAllTenants runs the sweep for every enabled, Square-connected
// tenant. Mirrors SyncAllTenants' skip/log behaviour. Cron entry point.
func (a *App) ReconcileAllTenants(ctx context.Context) error {
	tenantIDs, err := listSquareTenants(ctx, a.pool)
	if err != nil {
		return err
	}
	for _, tid := range tenantIDs {
		if !apps.IsEnabled(ctx, tid, AppName) {
			continue
		}
		if _, err := a.RunReconcile(ctx, tid); err != nil {
			if errors.Is(err, googlecalendar.ErrNotConfigured) || errors.Is(err, square.ErrNotConfigured) {
				continue
			}
			slog.Warn("squareshifts: tenant reconcile failed", "tenant_id", tid, "error", err)
		}
	}
	return nil
}

// eventStartsInWindow reports whether a calendar event's start falls in
// [start, end). Unparseable/missing starts return false so we never delete
// an event we can't place.
func eventStartsInWindow(e googlecalendar.Event, start, end time.Time) bool {
	t, ok := eventStart(e)
	if !ok {
		return false
	}
	return !t.Before(start) && t.Before(end)
}

// eventStart returns an event's start instant. Timed events carry an RFC 3339
// DateTime; all-day events (what this sync writes) carry only a "2006-01-02"
// Date, which we place at UTC midnight — precise enough to decide window
// membership over a multi-week horizon.
func eventStart(e googlecalendar.Event) (time.Time, bool) {
	if e.Start == nil {
		return time.Time{}, false
	}
	if e.Start.DateTime != "" {
		t, err := time.Parse(time.RFC3339, e.Start.DateTime)
		if err != nil {
			return time.Time{}, false
		}
		return t.UTC(), true
	}
	if e.Start.Date != "" {
		t, err := time.Parse("2006-01-02", e.Start.Date)
		if err != nil {
			return time.Time{}, false
		}
		return t, true // time.Parse of a bare date yields UTC
	}
	return time.Time{}, false
}

// privateProp reads one private extended property off an event, "" if absent.
func privateProp(e googlecalendar.Event, key string) string {
	if e.ExtendedProperties == nil {
		return ""
	}
	return e.ExtendedProperties.Private[key]
}
