package calendar

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/services"
)

// CalendarService manages configured calendars and their events.
type CalendarService struct {
	pool *pgxpool.Pool
}

// ConfigureOpts holds the inputs to Configure.
type ConfigureOpts struct {
	Name       string
	URL        string
	Timezone   string
	RoleScopes []string
}

// Configure adds (or updates) a calendar source. Admin-only. Validates the
// URL by performing an initial fetch+parse before persisting.
func (s *CalendarService) Configure(ctx context.Context, c *services.Caller, opts ConfigureOpts) (*Calendar, error) {
	if !c.IsAdmin {
		return nil, services.ErrForbidden
	}
	if opts.Name == "" {
		return nil, errors.New("name is required")
	}
	if opts.URL == "" {
		return nil, errors.New("url is required")
	}
	tz := opts.Timezone
	if tz == "" {
		tz = "UTC"
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return nil, fmt.Errorf("invalid timezone %q: %w", tz, err)
	}

	// Validate by fetching once before persisting.
	events, err := fetchAndParseFn(ctx, nil, opts.URL)
	if err != nil {
		return nil, fmt.Errorf("validating calendar URL: %w", err)
	}

	cal, err := upsertCalendar(ctx, s.pool, c.TenantID, opts.Name, opts.URL, tz)
	if err != nil {
		return nil, err
	}

	// Reset scopes on each (re)configure.
	if err := deleteCalendarScopes(ctx, s.pool, c.TenantID, cal.ID); err != nil {
		return nil, err
	}
	if len(opts.RoleScopes) == 0 {
		if err := addCalendarScope(ctx, s.pool, c.TenantID, cal.ID, "tenant", "*"); err != nil {
			return nil, err
		}
	} else {
		for _, role := range opts.RoleScopes {
			if err := addCalendarScope(ctx, s.pool, c.TenantID, cal.ID, "role", role); err != nil {
				return nil, err
			}
		}
	}

	// Persist the events from the validation fetch and record sync status.
	upsertErr := upsertEvents(ctx, s.pool, c.TenantID, cal.ID, events)
	if recErr := recordSyncResult(ctx, s.pool, c.TenantID, cal.ID, upsertErr); recErr != nil {
		slog.Error("recording calendar sync result", "calendar_id", cal.ID, "error", recErr)
	}
	if upsertErr != nil {
		return nil, fmt.Errorf("storing initial events: %w", upsertErr)
	}

	// Re-read so the caller sees fresh sync status.
	return getCalendarByID(ctx, s.pool, c.TenantID, cal.ID)
}

// Delete removes a configured calendar. Admin-only.
func (s *CalendarService) Delete(ctx context.Context, c *services.Caller, calendarID uuid.UUID) error {
	if !c.IsAdmin {
		return services.ErrForbidden
	}
	cal, err := getCalendarByID(ctx, s.pool, c.TenantID, calendarID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return services.ErrNotFound
		}
		return fmt.Errorf("looking up calendar: %w", err)
	}
	return deleteCalendar(ctx, s.pool, c.TenantID, cal.ID)
}

// CalendarWithStats bundles a Calendar with derived counts for list_calendars.
type CalendarWithStats struct {
	Calendar
	EventCount int `json:"event_count"`
}

// List returns calendars the caller can see, each with an event count.
func (s *CalendarService) List(ctx context.Context, c *services.Caller) ([]CalendarWithStats, error) {
	var cals []Calendar
	var err error
	if c.IsAdmin {
		cals, err = listCalendarsAll(ctx, s.pool, c.TenantID)
	} else {
		cals, err = listCalendarsScoped(ctx, s.pool, c.TenantID, c.Roles)
	}
	if err != nil {
		return nil, err
	}

	out := make([]CalendarWithStats, 0, len(cals))
	for _, cal := range cals {
		n, err := eventCountByCalendar(ctx, s.pool, cal.TenantID, cal.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, CalendarWithStats{Calendar: cal, EventCount: n})
	}
	return out, nil
}

// GetEventsOpts is the user-facing query input.
type GetEventsOpts struct {
	CalendarID string // optional UUID
	After      string // YYYY-MM-DD, defaults to today
	Before     string // YYYY-MM-DD, defaults to today + 7 days
	Query      string
	Limit      int
}

// GetEvents returns events visible to the caller within the date range.
func (s *CalendarService) GetEvents(ctx context.Context, c *services.Caller, opts GetEventsOpts) ([]Event, error) {
	visible, err := s.List(ctx, c)
	if err != nil {
		return nil, err
	}
	if len(visible) == 0 {
		return nil, nil
	}

	var ids []uuid.UUID
	if opts.CalendarID != "" {
		want, err := uuid.Parse(opts.CalendarID)
		if err != nil {
			return nil, errors.New("invalid calendar_id")
		}
		found := false
		for _, cs := range visible {
			if cs.ID == want {
				ids = append(ids, want)
				found = true
				break
			}
		}
		if !found {
			return nil, services.ErrForbidden
		}
	} else {
		for _, cs := range visible {
			ids = append(ids, cs.ID)
		}
	}

	now := time.Now()
	after := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if opts.After != "" {
		t, err := time.Parse("2006-01-02", opts.After)
		if err != nil {
			return nil, errors.New("invalid after date, use YYYY-MM-DD")
		}
		after = t
	}
	before := after.Add(7 * 24 * time.Hour)
	if opts.Before != "" {
		t, err := time.Parse("2006-01-02", opts.Before)
		if err != nil {
			return nil, errors.New("invalid before date, use YYYY-MM-DD")
		}
		// Make 'before' inclusive of the whole day.
		before = t.Add(24 * time.Hour)
	}

	return queryEvents(ctx, s.pool, c.TenantID, QueryEventsOpts{
		CalendarIDs: ids,
		After:       after,
		Before:      before,
		Query:       opts.Query,
		Limit:       opts.Limit,
	})
}

// SyncAllCalendars iterates every calendar across all tenants and syncs it.
// Errors on individual calendars are recorded and logged but do not abort the run.
func (s *CalendarService) SyncAllCalendars(ctx context.Context) error {
	cals, err := listAllCalendarsAcrossTenants(ctx, s.pool)
	if err != nil {
		return err
	}
	for i := range cals {
		cal := cals[i]
		syncErr := s.syncOne(ctx, &cal)
		if recErr := recordSyncResult(ctx, s.pool, cal.TenantID, cal.ID, syncErr); recErr != nil {
			slog.Error("recording calendar sync result", "calendar_id", cal.ID, "error", recErr)
		}
		if syncErr != nil {
			slog.Warn("calendar sync failed", "calendar_id", cal.ID, "name", cal.Name, "error", syncErr)
		}
	}
	return nil
}
