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

// syncWindowDays is the rolling horizon synced each run: from the start of
// today (UTC) forward. Past shifts fall out of the window and their events
// age out naturally rather than being pruned.
const syncWindowDays = 21

// SyncSummary counts what one run changed.
type SyncSummary struct {
	Created int
	Updated int
	Deleted int
}

// RunSync executes a sync for one tenant and records the outcome to
// audit_events. triggeredBy is "schedule" or "manual".
func (a *App) RunSync(ctx context.Context, tenantID uuid.UUID, triggeredBy string) (SyncSummary, error) {
	started := time.Now()
	sum, err := a.syncTenant(ctx, tenantID)
	if err != nil {
		a.auditFailed(ctx, tenantID, triggeredBy, err, time.Since(started))
		return sum, err
	}
	a.auditCompleted(ctx, tenantID, triggeredBy, sum, time.Since(started))
	return sum, nil
}

// syncTenant pulls the published Square schedule for the rolling window,
// upserts a Google Calendar event per shift, and deletes events whose shift
// has vanished from the window. Unchanged shifts (matching content hash) are
// skipped.
func (a *App) syncTenant(ctx context.Context, tenantID uuid.UUID) (SyncSummary, error) {
	var sum SyncSummary

	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, syncWindowDays)

	shifts, err := square.Instance().ListPublishedShifts(ctx, tenantID, start, end)
	if err != nil {
		return sum, fmt.Errorf("pulling square shifts: %w", err)
	}
	gcal, calendarID, err := googlecalendar.Instance().LoadClient(ctx, tenantID)
	if err != nil {
		return sum, fmt.Errorf("loading google calendar: %w", err)
	}

	seen := make(map[string]bool, len(shifts))
	for _, s := range shifts {
		if s.ShiftID == "" {
			continue
		}
		seen[s.ShiftID] = true

		ev := buildEvent(s, tenantID)
		hash := contentHash(ev)
		existing, err := getMapping(ctx, a.pool, tenantID, s.ShiftID)
		if err != nil {
			return sum, err
		}
		if existing != nil && existing.ContentHash == hash {
			continue // already current
		}

		if _, err := gcal.UpsertEvent(ctx, calendarID, ev); err != nil {
			return sum, fmt.Errorf("writing event for shift %s: %w", s.ShiftID, err)
		}
		startAt, perr := time.Parse(time.RFC3339, s.StartAt)
		if perr != nil {
			startAt = start // fall back to window start rather than failing the run
		}
		if err := upsertMapping(ctx, a.pool, tenantID, s.ShiftID, ev.ID, startAt, 0, hash); err != nil {
			return sum, err
		}
		if existing == nil {
			sum.Created++
		} else {
			sum.Updated++
		}
	}

	// Delete events for shifts that were in this window but are no longer
	// published (removed or unpublished in Square).
	windowMappings, err := listMappingsInWindow(ctx, a.pool, tenantID, start, end)
	if err != nil {
		return sum, err
	}
	for _, m := range windowMappings {
		if seen[m.ShiftID] {
			continue
		}
		if err := gcal.DeleteEvent(ctx, calendarID, m.GoogleEventID); err != nil {
			return sum, fmt.Errorf("deleting event for shift %s: %w", m.ShiftID, err)
		}
		if err := deleteMapping(ctx, a.pool, tenantID, m.ShiftID); err != nil {
			return sum, err
		}
		sum.Deleted++
	}
	return sum, nil
}

// SyncAllTenants runs the sync for every enabled tenant that has a Square
// integration. Tenants missing a Google Calendar connection (or with the
// app disabled) are skipped quietly; other errors are logged and the sweep
// continues to the next tenant. This is the cron entry point.
func (a *App) SyncAllTenants(ctx context.Context) error {
	tenantIDs, err := listSquareTenants(ctx, a.pool)
	if err != nil {
		return err
	}
	for _, tid := range tenantIDs {
		if !apps.IsEnabled(ctx, tid, AppName) {
			continue
		}
		if _, err := a.RunSync(ctx, tid, "schedule"); err != nil {
			if errors.Is(err, googlecalendar.ErrNotConfigured) || errors.Is(err, square.ErrNotConfigured) {
				continue
			}
			slog.Warn("squareshifts: tenant sync failed", "tenant_id", tid, "error", err)
		}
	}
	return nil
}
