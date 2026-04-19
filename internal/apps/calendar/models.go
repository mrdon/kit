package calendar

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
)

// Calendar is a configured iCal source.
type Calendar struct {
	ID             uuid.UUID  `json:"id"`
	TenantID       uuid.UUID  `json:"tenant_id"`
	Name           string     `json:"name"`
	URL            string     `json:"url"`
	Timezone       string     `json:"timezone"`
	LastSyncAt     *time.Time `json:"last_sync_at"`
	LastSyncStatus string     `json:"last_sync_status"`
	LastSyncError  *string    `json:"last_sync_error"`
	CreatedAt      time.Time  `json:"created_at"`
}

// Event is one occurrence parsed from a calendar feed.
type Event struct {
	ID          uuid.UUID `json:"id"`
	TenantID    uuid.UUID `json:"tenant_id"`
	CalendarID  uuid.UUID `json:"calendar_id"`
	UID         string    `json:"uid"`
	Summary     string    `json:"summary"`
	Description string    `json:"description"`
	Location    string    `json:"location"`
	StartTime   time.Time `json:"start_time"`
	EndTime     time.Time `json:"end_time"`
	AllDay      bool      `json:"all_day"`
}

func upsertCalendar(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, name, rawURL, tz string) (*Calendar, error) {
	c := &Calendar{TenantID: tenantID, Name: name, URL: rawURL, Timezone: tz}
	err := pool.QueryRow(ctx, `
		INSERT INTO app_calendars (tenant_id, name, url, timezone)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (tenant_id, name) DO UPDATE
			SET url = EXCLUDED.url, timezone = EXCLUDED.timezone
		RETURNING id, last_sync_status, created_at`,
		tenantID, name, rawURL, tz,
	).Scan(&c.ID, &c.LastSyncStatus, &c.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("upserting calendar: %w", err)
	}
	return c, nil
}

func deleteCalendar(ctx context.Context, pool *pgxpool.Pool, tenantID, calendarID uuid.UUID) error {
	_, err := pool.Exec(ctx, `
		DELETE FROM app_calendars WHERE tenant_id = $1 AND id = $2`,
		tenantID, calendarID,
	)
	if err != nil {
		return fmt.Errorf("deleting calendar: %w", err)
	}
	return nil
}

func addCalendarScope(ctx context.Context, pool *pgxpool.Pool, tenantID, calendarID uuid.UUID, roleID, userID *uuid.UUID) error {
	scopeID, err := models.GetOrCreateScope(ctx, pool, tenantID, roleID, userID)
	if err != nil {
		return fmt.Errorf("get-or-create scope: %w", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO app_calendar_scopes (tenant_id, calendar_id, scope_id)
		VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING`,
		tenantID, calendarID, scopeID,
	)
	if err != nil {
		return fmt.Errorf("adding calendar scope: %w", err)
	}
	return nil
}

func deleteCalendarScopes(ctx context.Context, pool *pgxpool.Pool, tenantID, calendarID uuid.UUID) error {
	_, err := pool.Exec(ctx, `
		DELETE FROM app_calendar_scopes WHERE tenant_id = $1 AND calendar_id = $2`,
		tenantID, calendarID,
	)
	if err != nil {
		return fmt.Errorf("deleting calendar scopes: %w", err)
	}
	return nil
}

const calendarSelectCols = `c.id, c.tenant_id, c.name, c.url, c.timezone, c.last_sync_at, c.last_sync_status, c.last_sync_error, c.created_at`

func listCalendarsAll(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) ([]Calendar, error) {
	rows, err := pool.Query(ctx, `
		SELECT `+calendarSelectCols+`
		FROM app_calendars c
		WHERE c.tenant_id = $1
		ORDER BY c.name`,
		tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("listing calendars: %w", err)
	}
	defer rows.Close()
	return scanCalendars(rows)
}

func listCalendarsScoped(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID, roleIDs []uuid.UUID) ([]Calendar, error) {
	scopeSQL, scopeArgs := models.ScopeFilterIDs("sc", 2, userID, roleIDs)
	args := append([]any{tenantID}, scopeArgs...)
	rows, err := pool.Query(ctx, `
		SELECT DISTINCT `+calendarSelectCols+`
		FROM app_calendars c
		JOIN app_calendar_scopes cs ON cs.calendar_id = c.id AND cs.tenant_id = c.tenant_id
		JOIN scopes sc ON sc.id = cs.scope_id
		WHERE c.tenant_id = $1
		AND (`+scopeSQL+`)
		ORDER BY c.name`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("listing scoped calendars: %w", err)
	}
	defer rows.Close()
	return scanCalendars(rows)
}

func getCalendarByID(ctx context.Context, pool *pgxpool.Pool, tenantID, calendarID uuid.UUID) (*Calendar, error) {
	var c Calendar
	err := pool.QueryRow(ctx, `
		SELECT `+calendarSelectCols+`
		FROM app_calendars c
		WHERE c.tenant_id = $1 AND c.id = $2`,
		tenantID, calendarID,
	).Scan(&c.ID, &c.TenantID, &c.Name, &c.URL, &c.Timezone, &c.LastSyncAt, &c.LastSyncStatus, &c.LastSyncError, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func listAllCalendarsAcrossTenants(ctx context.Context, pool *pgxpool.Pool) ([]Calendar, error) {
	rows, err := pool.Query(ctx, `
		SELECT `+calendarSelectCols+`
		FROM app_calendars c
		ORDER BY c.tenant_id, c.name`)
	if err != nil {
		return nil, fmt.Errorf("listing all calendars: %w", err)
	}
	defer rows.Close()
	return scanCalendars(rows)
}

func scanCalendars(rows pgx.Rows) ([]Calendar, error) {
	var out []Calendar
	for rows.Next() {
		var c Calendar
		if err := rows.Scan(&c.ID, &c.TenantID, &c.Name, &c.URL, &c.Timezone, &c.LastSyncAt, &c.LastSyncStatus, &c.LastSyncError, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning calendar: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func recordSyncResult(ctx context.Context, pool *pgxpool.Pool, tenantID, calendarID uuid.UUID, syncErr error) error {
	status := "ok"
	var errStr *string
	if syncErr != nil {
		status = "error"
		s := syncErr.Error()
		errStr = &s
	}
	_, err := pool.Exec(ctx, `
		UPDATE app_calendars
		SET last_sync_at = now(), last_sync_status = $3, last_sync_error = $4
		WHERE tenant_id = $1 AND id = $2`,
		tenantID, calendarID, status, errStr,
	)
	if err != nil {
		return fmt.Errorf("recording sync result: %w", err)
	}
	return nil
}

func upsertEvents(ctx context.Context, pool *pgxpool.Pool, tenantID, calendarID uuid.UUID, events []Event) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	seen := make([]string, 0, len(events))
	for _, e := range events {
		seen = append(seen, e.UID)
		_, err := tx.Exec(ctx, `
			INSERT INTO app_calendar_events
				(tenant_id, calendar_id, uid, summary, description, location, start_time, end_time, all_day, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
			ON CONFLICT (tenant_id, calendar_id, uid) DO UPDATE SET
				summary = EXCLUDED.summary,
				description = EXCLUDED.description,
				location = EXCLUDED.location,
				start_time = EXCLUDED.start_time,
				end_time = EXCLUDED.end_time,
				all_day = EXCLUDED.all_day,
				updated_at = now()`,
			tenantID, calendarID, e.UID, e.Summary, e.Description, e.Location, e.StartTime, e.EndTime, e.AllDay,
		)
		if err != nil {
			return fmt.Errorf("upserting event %s: %w", e.UID, err)
		}
	}

	// Delete events that have disappeared from the feed.
	if len(seen) > 0 {
		_, err = tx.Exec(ctx, `
			DELETE FROM app_calendar_events
			WHERE tenant_id = $1 AND calendar_id = $2 AND uid <> ALL($3)`,
			tenantID, calendarID, seen,
		)
	} else {
		_, err = tx.Exec(ctx, `
			DELETE FROM app_calendar_events
			WHERE tenant_id = $1 AND calendar_id = $2`,
			tenantID, calendarID,
		)
	}
	if err != nil {
		return fmt.Errorf("pruning events: %w", err)
	}

	return tx.Commit(ctx)
}

// QueryEventsOpts filters events for GetEvents.
type QueryEventsOpts struct {
	CalendarIDs []uuid.UUID // empty = all visible
	After       time.Time
	Before      time.Time
	Query       string
	Limit       int
}

func queryEvents(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, opts QueryEventsOpts) ([]Event, error) {
	if len(opts.CalendarIDs) == 0 {
		return nil, nil
	}
	args := []any{tenantID, opts.CalendarIDs, opts.After, opts.Before}
	sql := `
		SELECT id, tenant_id, calendar_id, uid, summary, description, location, start_time, end_time, all_day
		FROM app_calendar_events
		WHERE tenant_id = $1
		  AND calendar_id = ANY($2)
		  AND start_time <= $4
		  AND end_time >= $3`
	if opts.Query != "" {
		args = append(args, opts.Query)
		sql += fmt.Sprintf(`
		  AND to_tsvector('english', coalesce(summary,'') || ' ' || coalesce(description,'') || ' ' || coalesce(location,''))
		      @@ plainto_tsquery('english', $%d)`, len(args))
	}
	limit := opts.Limit
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	args = append(args, limit)
	sql += fmt.Sprintf(`
		ORDER BY start_time
		LIMIT $%d`, len(args))

	rows, err := pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("querying events: %w", err)
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.TenantID, &e.CalendarID, &e.UID, &e.Summary, &e.Description, &e.Location, &e.StartTime, &e.EndTime, &e.AllDay); err != nil {
			return nil, fmt.Errorf("scanning event: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func eventCountByCalendar(ctx context.Context, pool *pgxpool.Pool, tenantID, calendarID uuid.UUID) (int, error) {
	var n int
	err := pool.QueryRow(ctx, `
		SELECT count(*) FROM app_calendar_events WHERE tenant_id = $1 AND calendar_id = $2`,
		tenantID, calendarID,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting events: %w", err)
	}
	return n, nil
}
