package events

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/googlecalendar"
)

// ReconcilePlan is what a sweep would change. Building it is read-only, so it
// doubles as the dry run.
type ReconcilePlan struct {
	// Create are events Kit expects on the calendar that are not there --
	// typically someone deleted the entry in Google.
	Create []Event
	// Delete are calendar entries this app owns that no longer correspond to a
	// live event.
	Delete []googlecalendar.Event

	calendarID string
}

// Empty reports whether the calendar already matches Kit.
func (p ReconcilePlan) Empty() bool { return len(p.Create) == 0 && len(p.Delete) == 0 }

// PreviewReconcile computes what a sweep would do without touching anything.
//
// This exists because reconcile is the only path in the app that deletes
// calendar entries, and the operator should be able to see which ones before
// it runs.
func (a *App) PreviewReconcile(ctx context.Context, tenantID uuid.UUID) (ReconcilePlan, error) {
	writer, settings, err := a.writerFor(ctx, tenantID)
	if err != nil {
		return ReconcilePlan{}, err
	}
	return a.planReconcile(ctx, writer, settings, tenantID)
}

// RunReconcile repairs drift for one tenant.
func (a *App) RunReconcile(ctx context.Context, tenantID uuid.UUID) (Summary, error) {
	started := time.Now()
	writer, settings, err := a.writerFor(ctx, tenantID)
	if err != nil {
		return Summary{}, err
	}
	plan, err := a.planReconcile(ctx, writer, settings, tenantID)
	if err != nil {
		a.auditFailed(ctx, tenantID, "reconcile", err, time.Since(started))
		return Summary{}, err
	}
	sum, err := a.applyReconcile(ctx, writer, settings, plan)
	if err != nil {
		a.auditFailed(ctx, tenantID, "reconcile", err, time.Since(started))
		return sum, err
	}
	// A clean pass is the normal case; only record one that repaired something.
	if sum.changed() {
		a.auditCompleted(ctx, tenantID, "reconcile", sum, time.Since(started))
	}
	return sum, nil
}

// planReconcile diffs Kit against the calendar.
//
// Two rules make this safe, and both are easy to get wrong:
//
// 1. `actual` is queried by OWNERSHIP STAMP, never by listing the calendar.
// Anything without this app's stamp -- a human's meeting, the shift sync's
// entries, another tenant's -- is invisible here and can never be deleted.
//
// 2. There is NO time window. The shift sync spares events starting outside a
// rolling window, because Square is only queried for that window so absence
// outside it is uninformative. Copying that here would be a bug: a Google
// recurring master carries the start of its FIRST occurrence, so a weekly
// trivia that began two years ago sits far outside any window and its orphaned
// series would be spared forever. Kit owns these rows, so `desired` is complete
// by construction and no window is needed.
func (a *App) planReconcile(ctx context.Context, writer calendarWriter, settings Settings, tenantID uuid.UUID) (ReconcilePlan, error) {
	plan := ReconcilePlan{calendarID: settings.CalendarID}

	owned, err := writer.ListEventsByPrivateProperties(ctx, settings.CalendarID,
		googlecalendar.OwnerProps(AppName, tenantID))
	if err != nil {
		return plan, fmt.Errorf("listing owned calendar entries: %w", err)
	}

	events, err := listSyncCandidates(ctx, a.pool, tenantID)
	if err != nil {
		return plan, err
	}

	desired := make(map[string]*Event, len(events))
	for i := range events {
		e := &events[i]
		if len(desiredCalendars(e, settings)) > 0 {
			desired[googleEventID(e.ID)] = e
		}
	}

	present := make(map[string]bool, len(owned))
	for _, ev := range owned {
		present[ev.ID] = true
		if _, want := desired[ev.ID]; !want {
			plan.Delete = append(plan.Delete, ev)
		}
	}
	for id, e := range desired {
		if !present[id] {
			plan.Create = append(plan.Create, *e)
		}
	}
	return plan, nil
}

func (a *App) applyReconcile(ctx context.Context, writer calendarWriter, settings Settings, plan ReconcilePlan) (Summary, error) {
	var sum Summary

	for _, ev := range plan.Delete {
		if err := writer.DeleteEvent(ctx, plan.calendarID, ev.ID); err != nil {
			return sum, fmt.Errorf("removing orphaned calendar entry %s: %w", ev.ID, err)
		}
		sum.Deleted++
	}
	for i := range plan.Create {
		e := &plan.Create[i]
		// Clear the stale handle so syncOne treats this as a fresh write
		// rather than trusting a hash for an entry that is no longer there.
		e.GCalEventID = ""
		e.GCalContentHash = ""
		if _, err := a.syncOne(ctx, writer, settings, e); err != nil {
			return sum, err
		}
		sum.Created++
	}
	return sum, nil
}

// FormatReconcilePlan renders a dry run. Deletions are itemised by name
// because "would remove 4 entries" is not something an operator can approve.
func FormatReconcilePlan(plan ReconcilePlan) string {
	if plan.Empty() {
		return "The calendar already matches Kit; nothing to repair."
	}
	var b strings.Builder
	b.WriteString("Reconcile would make these changes:\n")
	for _, ev := range plan.Delete {
		fmt.Fprintf(&b, "  - remove from the calendar: %s\n", ev.Summary)
	}
	for i := range plan.Create {
		fmt.Fprintf(&b, "  - restore to the calendar: %s\n", plan.Create[i].Title)
	}
	b.WriteString("\nOnly entries this app created are ever removed; anything else on the calendar is untouched.\n")
	return b.String()
}
