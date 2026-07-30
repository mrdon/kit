package events

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when no event matches within the tenant.
var ErrNotFound = errors.New("event not found")

// Event is one row of app_events. See migration 070 for why the three
// classification axes are separate columns rather than one type enum.
type Event struct {
	ID       uuid.UUID `json:"id"`
	TenantID uuid.UUID `json:"tenant_id"`

	Title       string `json:"title"`
	Slug        string `json:"slug"`
	Summary     string `json:"summary,omitempty"`
	Description string `json:"description,omitempty"`
	// PrepNotes is the internal bartender brief. It reaches the calendar
	// (staff and the food partner read that) but never the public feed.
	PrepNotes string `json:"prep_notes,omitempty"`

	StartsAt time.Time  `json:"starts_at"`
	EndsAt   *time.Time `json:"ends_at,omitempty"`
	AllDay   bool       `json:"all_day"`
	// Timezone is a named IANA zone. Never store a fixed offset: the zone is
	// what keeps a weekly 7pm event at 7pm across a DST boundary.
	Timezone string `json:"timezone"`
	RRule    string `json:"rrule,omitempty"`

	Location         string     `json:"location,omitempty"`
	HeroAttachmentID *uuid.UUID `json:"hero_attachment_id,omitempty"`

	Status      Status      `json:"status"`
	Visibility  Visibility  `json:"visibility"`
	Venue       Venue       `json:"venue"`
	SpaceImpact SpaceImpact `json:"space_impact"`

	NotifyFoodPartner bool `json:"notify_food_partner"`

	PriceCents         *int64 `json:"price_cents,omitempty"`
	Currency           string `json:"currency"`
	Capacity           *int   `json:"capacity,omitempty"`
	ExpectedAttendance *int   `json:"expected_attendance,omitempty"`
	RegistrationURL    string `json:"registration_url,omitempty"`
	SquareVariationID  string `json:"square_variation_id,omitempty"`

	// Calendar sync state. GCalCalendarID records where the event was actually
	// written, which is how a settings change is detected as orphaning.
	GCalEventID     string `json:"-"`
	GCalCalendarID  string `json:"-"`
	GCalContentHash string `json:"-"`

	CreatedBy *uuid.UUID `json:"created_by,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// Loc resolves the event's named zone, falling back to UTC so a bad value
// degrades to a wrong-but-stable rendering rather than a nil dereference deep
// in the expander. (Named Loc, not Location, because Location is the venue.)
func (e *Event) Loc() *time.Location {
	if e == nil || e.Timezone == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(e.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// End returns the event's end, defaulting to a one-hour block when unset so
// calendar writes always carry both endpoints.
func (e *Event) End() time.Time {
	if e.EndsAt != nil {
		return *e.EndsAt
	}
	return e.StartsAt.Add(time.Hour)
}

// Rule parses the stored recurrence. A malformed value returns nil rather than
// an error: writes are validated, so anything stored is already known-good,
// and a read path should not fail because of a hand-edited row.
func (e *Event) Rule() *Rule {
	if e == nil || e.RRule == "" {
		return nil
	}
	r, err := ParseRule(e.RRule)
	if err != nil {
		return nil
	}
	return r
}

// Occurrences expands the event within [from, to). Non-recurring events yield
// at most one, so callers need no special case.
func (e *Event) Occurrences(from, to time.Time) []Occurrence {
	return Expand(e.StartsAt, e.End(), e.Loc(), e.Rule(), from, to)
}

const eventColumns = `
	id, tenant_id, title, slug, summary, description, prep_notes,
	starts_at, ends_at, all_day, timezone, rrule,
	location, hero_attachment_id,
	status, visibility, venue, space_impact, notify_food_partner,
	price_cents, currency, capacity, expected_attendance,
	registration_url, square_variation_id,
	gcal_event_id, gcal_calendar_id, gcal_content_hash,
	created_by, created_at, updated_at`

func scanEvent(row pgx.Row) (*Event, error) {
	var e Event
	var rrule, registrationURL, squareVariationID *string
	err := row.Scan(
		&e.ID, &e.TenantID, &e.Title, &e.Slug, &e.Summary, &e.Description, &e.PrepNotes,
		&e.StartsAt, &e.EndsAt, &e.AllDay, &e.Timezone, &rrule,
		&e.Location, &e.HeroAttachmentID,
		&e.Status, &e.Visibility, &e.Venue, &e.SpaceImpact, &e.NotifyFoodPartner,
		&e.PriceCents, &e.Currency, &e.Capacity, &e.ExpectedAttendance,
		&registrationURL, &squareVariationID,
		&e.GCalEventID, &e.GCalCalendarID, &e.GCalContentHash,
		&e.CreatedBy, &e.CreatedAt, &e.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	e.RRule = derefString(rrule)
	e.RegistrationURL = derefString(registrationURL)
	e.SquareVariationID = derefString(squareVariationID)
	return &e, nil
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// nilIfEmpty maps "" to NULL so nullable text columns stay genuinely null
// rather than holding empty strings that read as "set but blank".
func nilIfEmpty(s string) *string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return &s
}

func insertEvent(ctx context.Context, pool *pgxpool.Pool, e *Event) (*Event, error) {
	row := pool.QueryRow(ctx, `
		INSERT INTO app_events (
			tenant_id, title, slug, summary, description, prep_notes,
			starts_at, ends_at, all_day, timezone, rrule,
			location, hero_attachment_id,
			status, visibility, venue, space_impact, notify_food_partner,
			price_cents, currency, capacity, expected_attendance,
			registration_url, square_variation_id, created_by
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11,
			$12, $13,
			$14, $15, $16, $17, $18,
			$19, $20, $21, $22,
			$23, $24, $25
		)
		RETURNING `+eventColumns,
		e.TenantID, e.Title, e.Slug, e.Summary, e.Description, e.PrepNotes,
		e.StartsAt, e.EndsAt, e.AllDay, e.Timezone, nilIfEmpty(e.RRule),
		e.Location, e.HeroAttachmentID,
		e.Status, e.Visibility, e.Venue, e.SpaceImpact, e.NotifyFoodPartner,
		e.PriceCents, e.Currency, e.Capacity, e.ExpectedAttendance,
		nilIfEmpty(e.RegistrationURL), nilIfEmpty(e.SquareVariationID), e.CreatedBy,
	)
	out, err := scanEvent(row)
	if err != nil {
		return nil, fmt.Errorf("inserting event: %w", err)
	}
	return out, nil
}

// updateEvent writes every mutable column. Callers load, mutate, and save, so
// partial-patch semantics live at the service layer where the caller's intent
// (field present vs absent) is still known.
func updateEvent(ctx context.Context, pool *pgxpool.Pool, e *Event) (*Event, error) {
	row := pool.QueryRow(ctx, `
		UPDATE app_events SET
			title = $3, slug = $4, summary = $5, description = $6, prep_notes = $7,
			starts_at = $8, ends_at = $9, all_day = $10, timezone = $11, rrule = $12,
			location = $13, hero_attachment_id = $14,
			status = $15, visibility = $16, venue = $17, space_impact = $18,
			notify_food_partner = $19,
			price_cents = $20, currency = $21, capacity = $22, expected_attendance = $23,
			registration_url = $24, square_variation_id = $25,
			updated_at = now()
		WHERE tenant_id = $1 AND id = $2
		RETURNING `+eventColumns,
		e.TenantID, e.ID,
		e.Title, e.Slug, e.Summary, e.Description, e.PrepNotes,
		e.StartsAt, e.EndsAt, e.AllDay, e.Timezone, nilIfEmpty(e.RRule),
		e.Location, e.HeroAttachmentID,
		e.Status, e.Visibility, e.Venue, e.SpaceImpact,
		e.NotifyFoodPartner,
		e.PriceCents, e.Currency, e.Capacity, e.ExpectedAttendance,
		nilIfEmpty(e.RegistrationURL), nilIfEmpty(e.SquareVariationID),
	)
	out, err := scanEvent(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("updating event: %w", err)
	}
	return out, nil
}

func getEvent(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID) (*Event, error) {
	row := pool.QueryRow(ctx, `SELECT `+eventColumns+`
		FROM app_events WHERE tenant_id = $1 AND id = $2`, tenantID, id)
	e, err := scanEvent(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("loading event: %w", err)
	}
	return e, nil
}

func getEventBySlug(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, slug string) (*Event, error) {
	row := pool.QueryRow(ctx, `SELECT `+eventColumns+`
		FROM app_events WHERE tenant_id = $1 AND slug = $2`, tenantID, slug)
	e, err := scanEvent(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("loading event by slug: %w", err)
	}
	return e, nil
}

// ListFilter narrows a listing. The zero value lists every event in the tenant,
// newest start first.
type ListFilter struct {
	Status     Status
	Visibility Visibility
	// From/To bound starts_at. Recurring rows are exempt from From (see
	// listEvents) because a weekly series' first occurrence may be long past.
	From  *time.Time
	To    *time.Time
	Limit int
}

func listEvents(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, f ListFilter) ([]Event, error) {
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	// The (rrule IS NOT NULL OR ...) clause is load-bearing, not defensive. A
	// weekly series stores its FIRST occurrence in starts_at, which for trivia
	// that began years ago is far in the past -- a naive lower bound would
	// silently drop it from every upcoming-events view forever.
	rows, err := pool.Query(ctx, `
		SELECT `+eventColumns+`
		FROM app_events
		WHERE tenant_id = $1
		  AND ($2::text IS NULL OR status = $2)
		  AND ($3::text IS NULL OR visibility = $3)
		  AND ($4::timestamptz IS NULL OR rrule IS NOT NULL OR coalesce(ends_at, starts_at) >= $4)
		  AND ($5::timestamptz IS NULL OR starts_at < $5)
		ORDER BY starts_at ASC
		LIMIT $6`,
		tenantID, nilIfEmpty(string(f.Status)), nilIfEmpty(string(f.Visibility)),
		f.From, f.To, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("listing events: %w", err)
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

// countEventsOnCalendar reports how many events still hold a handle on a given
// calendar. Used when an admin repoints the app at a different one, to warn
// about entries that would otherwise be left behind.
func countEventsOnCalendar(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, calendarID string) (int, error) {
	var n int
	err := pool.QueryRow(ctx, `
		SELECT count(*) FROM app_events
		WHERE tenant_id = $1 AND gcal_calendar_id = $2 AND gcal_event_id <> ''`,
		tenantID, calendarID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting events on calendar: %w", err)
	}
	return n, nil
}
