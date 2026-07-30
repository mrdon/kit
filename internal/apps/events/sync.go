package events

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/googlecalendar"
)

// ErrNoCalendar means the tenant has not picked a calendar yet. Not a failure
// -- just nothing to do.
var ErrNoCalendar = errors.New("no events calendar configured")

// ErrNotConfigured means the app was not wired up (no pool).
var ErrNotConfigured = errors.New("events app not configured")

// Summary counts what a run changed.
type Summary struct {
	Created int
	Updated int
	Deleted int
	Skipped int
}

func (s Summary) changed() bool { return s.Created+s.Updated+s.Deleted > 0 }

func (s Summary) String() string {
	return fmt.Sprintf("%d created, %d updated, %d removed, %d unchanged",
		s.Created, s.Updated, s.Deleted, s.Skipped)
}

// RunSync pushes pending changes for one tenant and records the outcome.
func (a *App) RunSync(ctx context.Context, tenantID uuid.UUID, triggeredBy string) (Summary, error) {
	started := time.Now()
	sum, err := a.syncTenant(ctx, tenantID)
	if err != nil {
		a.auditFailed(ctx, tenantID, triggeredBy, err, time.Since(started))
		return sum, err
	}
	// Record manual runs (the operator asked, so they want the feedback) and
	// any run that changed something. A no-op scheduled run every 15 minutes
	// would otherwise bury the audit log in noise.
	if triggeredBy == "manual" || sum.changed() {
		a.auditCompleted(ctx, tenantID, triggeredBy, sum, time.Since(started))
	}
	return sum, nil
}

// syncTenant walks every event that could need a calendar write.
//
// Unlike the shift sync, there is no rolling time window: Kit owns these rows,
// so the desired set is complete by construction. A window would also be
// actively wrong here -- a weekly series stores its FIRST occurrence, which for
// a long-running trivia night is years in the past.
func (a *App) syncTenant(ctx context.Context, tenantID uuid.UUID) (Summary, error) {
	var sum Summary

	writer, settings, err := a.writerFor(ctx, tenantID)
	if err != nil {
		return sum, err
	}
	events, err := listSyncCandidates(ctx, a.pool, tenantID)
	if err != nil {
		return sum, err
	}

	for i := range events {
		e := &events[i]
		delta, err := a.syncOne(ctx, writer, settings, e)
		if err != nil {
			return sum, err
		}
		switch delta {
		case deltaCreated:
			sum.Created++
		case deltaUpdated:
			sum.Updated++
		case deltaDeleted:
			sum.Deleted++
		case deltaNone:
			sum.Skipped++
		}
	}
	return sum, nil
}

type delta int

const (
	deltaNone delta = iota
	deltaCreated
	deltaUpdated
	deltaDeleted
)

// syncOne brings a single event's calendar copy in line with the row.
func (a *App) syncOne(ctx context.Context, writer calendarWriter, settings Settings, e *Event) (delta, error) {
	targets := desiredCalendars(e, settings)

	// Not wanted on any calendar. Remove the copy if we made one.
	if len(targets) == 0 {
		if e.GCalEventID == "" {
			return deltaNone, nil
		}
		// Google first, database second. The reverse order would leave a live
		// entry with no record that it exists -- the 15-minute sync would never
		// look at it again, and only the 12-hour reconcile could find it.
		if err := writer.DeleteEvent(ctx, e.GCalCalendarID, e.GCalEventID); err != nil {
			return deltaNone, fmt.Errorf("removing calendar entry for %s: %w", e.ID, err)
		}
		if err := clearCalendarState(ctx, a.pool, e.TenantID, e.ID); err != nil {
			return deltaNone, err
		}
		return deltaDeleted, nil
	}

	calendarID := targets[0]

	// The admin repointed the app at a different calendar. Drain the old copy
	// before writing the new one, or it is stranded there permanently:
	// reconcile only ever queries the currently configured calendar.
	if e.GCalEventID != "" && e.GCalCalendarID != "" && e.GCalCalendarID != calendarID {
		if err := writer.DeleteEvent(ctx, e.GCalCalendarID, e.GCalEventID); err != nil {
			return deltaNone, fmt.Errorf("draining %s from the previous calendar: %w", e.ID, err)
		}
		e.GCalEventID = ""
		e.GCalContentHash = ""
	}

	payload := buildEvent(e, e.TenantID)
	hash := contentHash(payload, calendarID)
	if e.GCalEventID != "" && e.GCalContentHash == hash {
		return deltaNone, nil
	}

	if _, err := writer.UpsertEvent(ctx, calendarID, payload); err != nil {
		return deltaNone, fmt.Errorf("writing calendar entry for %s: %w", e.ID, err)
	}
	if err := saveCalendarState(ctx, a.pool, e.TenantID, e.ID, payload.ID, calendarID, hash); err != nil {
		return deltaNone, err
	}
	if e.GCalEventID == "" {
		return deltaCreated, nil
	}
	return deltaUpdated, nil
}

// SyncNow runs a sync for one tenant on demand.
func (a *App) SyncNow(ctx context.Context, tenantID uuid.UUID) (Summary, error) {
	return a.RunSync(ctx, tenantID, "manual")
}

// SyncAllTenants is the cron entry point. Tenants without a calendar are
// skipped quietly; a failure on one tenant does not stop the sweep.
func (a *App) SyncAllTenants(ctx context.Context) error {
	if a.pool == nil {
		return nil
	}
	tenantIDs, err := listTenantsWithCalendar(ctx, a.pool)
	if err != nil {
		return err
	}
	for _, tid := range tenantIDs {
		if !apps.IsEnabled(ctx, tid, AppName) {
			continue
		}
		if _, err := a.RunSync(ctx, tid, "schedule"); err != nil {
			if errors.Is(err, ErrNoCalendar) || errors.Is(err, googlecalendar.ErrNotConfigured) {
				continue
			}
			slog.Error("events sync failed", "tenant_id", tid, "error", err)
		}
	}
	return nil
}

// listSyncCandidates returns every event whose calendar state may need work:
// the published ones that should be present, plus any row still holding a
// Google id that no longer should be.
func listSyncCandidates(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) ([]Event, error) {
	rows, err := pool.Query(ctx, `
		SELECT `+eventColumns+`
		FROM app_events
		WHERE tenant_id = $1
		  AND (status = 'published' OR gcal_event_id <> '')
		ORDER BY starts_at ASC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("listing sync candidates: %w", err)
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning event: %w", err)
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// saveCalendarState records where an event was written and the hash of what
// was written, so the next sync can skip unchanged rows and a later settings
// change can be detected as orphaning.
func saveCalendarState(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID, eventID, calendarID, hash string) error {
	_, err := pool.Exec(ctx, `
		UPDATE app_events
		SET gcal_event_id = $3, gcal_calendar_id = $4, gcal_content_hash = $5
		WHERE tenant_id = $1 AND id = $2`,
		tenantID, id, eventID, calendarID, hash)
	if err != nil {
		return fmt.Errorf("saving calendar state: %w", err)
	}
	return nil
}

// clearCalendarState forgets the Google copy after a successful delete.
func clearCalendarState(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID) error {
	return saveCalendarState(ctx, pool, tenantID, id, "", "", "")
}

// listTenantsWithCalendar returns tenants whose events calendar is configured.
// The sweep starts here so it never touches tenants that have not opted in by
// picking a calendar.
func listTenantsWithCalendar(ctx context.Context, pool *pgxpool.Pool) ([]uuid.UUID, error) {
	rows, err := pool.Query(ctx, `
		SELECT tenant_id FROM app_event_settings WHERE calendar_id <> ''`)
	if err != nil {
		return nil, fmt.Errorf("listing event tenants: %w", err)
	}
	defer rows.Close()

	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scanning tenant id: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
