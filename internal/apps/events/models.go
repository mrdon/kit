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
	// RDates are the ADDITIONAL dates the event happens on, for series no rule
	// can express. StartsAt remains the first occurrence, so these are always
	// strictly after it -- the service normalises the combined set on write.
	RDates []time.Time `json:"rdates,omitempty"`

	Location         string     `json:"location,omitempty"`
	HeroAttachmentID *uuid.UUID `json:"hero_attachment_id,omitempty"`

	Status      Status      `json:"status"`
	Visibility  Visibility  `json:"visibility"`
	Venue       Venue       `json:"venue"`
	SpaceImpact SpaceImpact `json:"space_impact"`

	NotifyFoodPartner bool `json:"notify_food_partner"`
	// Prominence is the editorial axis -- featured / normal / background. See
	// migration 079, and Prominence in visibility.go for why the default
	// rather than the extremes is the interesting value.
	Prominence Prominence `json:"prominence"`

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

// Series bundles everything the expander needs: the anchor, the rule, and any
// explicit extra dates.
func (e *Event) Series() Series {
	return Series{
		Start:  e.StartsAt,
		End:    e.End(),
		Loc:    e.Loc(),
		Rule:   e.Rule(),
		RDates: e.RDates,
	}
}

// Occurrences expands the event within [from, to). Non-recurring events yield
// at most one, so callers need no special case.
func (e *Event) Occurrences(from, to time.Time) []Occurrence {
	return e.Series().Expand(from, to)
}

// Repeats reports whether the event happens more than once, by either
// mechanism. The two are asked about together often enough -- formatting, the
// calendar briefing, the upcoming-list exemption -- that a single predicate
// keeps them from being checked inconsistently.
func (e *Event) Repeats() bool {
	return e != nil && (e.RRule != "" || len(e.RDates) > 0)
}

const eventColumns = `
	id, tenant_id, title, slug, summary, description, prep_notes,
	starts_at, ends_at, all_day, timezone, rrule, rdates,
	location, hero_attachment_id,
	status, visibility, venue, space_impact, notify_food_partner, prominence,
	price_cents, currency, capacity, expected_attendance,
	registration_url, square_variation_id,
	gcal_event_id, gcal_calendar_id, gcal_content_hash,
	created_by, created_at, updated_at`

func scanEvent(row pgx.Row) (*Event, error) {
	var e Event
	var rrule, registrationURL, squareVariationID *string
	err := row.Scan(
		&e.ID, &e.TenantID, &e.Title, &e.Slug, &e.Summary, &e.Description, &e.PrepNotes,
		&e.StartsAt, &e.EndsAt, &e.AllDay, &e.Timezone, &rrule, &e.RDates,
		&e.Location, &e.HeroAttachmentID,
		&e.Status, &e.Visibility, &e.Venue, &e.SpaceImpact, &e.NotifyFoodPartner, &e.Prominence,
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

// emptyIfNil keeps the rdates column NOT NULL. A nil Go slice would be written
// as SQL NULL, which the column refuses and which would also make every
// cardinality() check in the queries return NULL instead of 0.
func emptyIfNil(ts []time.Time) []time.Time {
	if ts == nil {
		return []time.Time{}
	}
	return ts
}

func insertEvent(ctx context.Context, pool *pgxpool.Pool, e *Event) (*Event, error) {
	row := pool.QueryRow(ctx, `
		INSERT INTO app_events (
			tenant_id, title, slug, summary, description, prep_notes,
			starts_at, ends_at, all_day, timezone, rrule, rdates,
			location, hero_attachment_id,
			status, visibility, venue, space_impact, notify_food_partner, prominence,
			price_cents, currency, capacity, expected_attendance,
			registration_url, square_variation_id, created_by
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10, $11, $12,
			$13, $14,
			$15, $16, $17, $18, $19, $20,
			$21, $22, $23, $24,
			$25, $26, $27
		)
		RETURNING `+eventColumns,
		e.TenantID, e.Title, e.Slug, e.Summary, e.Description, e.PrepNotes,
		e.StartsAt, e.EndsAt, e.AllDay, e.Timezone, nilIfEmpty(e.RRule), emptyIfNil(e.RDates),
		e.Location, e.HeroAttachmentID,
		e.Status, e.Visibility, e.Venue, e.SpaceImpact, e.NotifyFoodPartner, e.Prominence,
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
			rdates = $13,
			location = $14, hero_attachment_id = $15,
			status = $16, visibility = $17, venue = $18, space_impact = $19,
			notify_food_partner = $20, prominence = $21,
			price_cents = $22, currency = $23, capacity = $24, expected_attendance = $25,
			registration_url = $26, square_variation_id = $27,
			updated_at = now()
		WHERE tenant_id = $1 AND id = $2
		RETURNING `+eventColumns,
		e.TenantID, e.ID,
		e.Title, e.Slug, e.Summary, e.Description, e.PrepNotes,
		e.StartsAt, e.EndsAt, e.AllDay, e.Timezone, nilIfEmpty(e.RRule),
		emptyIfNil(e.RDates),
		e.Location, e.HeroAttachmentID,
		e.Status, e.Visibility, e.Venue, e.SpaceImpact,
		e.NotifyFoodPartner, e.Prominence,
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
	From *time.Time
	To   *time.Time
	// ExcludeCancelled drops cancelled rows. The upcoming view uses it: a
	// called-off event is not something anyone is planning around, so it only
	// belongs in the archive alongside the past ones.
	ExcludeCancelled bool
	Limit            int
}

func listEvents(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, f ListFilter) ([]Event, error) {
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	// The recurrence exemption in the lower-bound clause is load-bearing, not
	// defensive. A repeating event stores its FIRST occurrence in starts_at,
	// which for trivia that began years ago is far in the past -- a naive lower
	// bound would silently drop it from every upcoming-events view forever.
	//
	// A rule-based series is exempt outright, because expanding an RRULE in SQL
	// is not on the table. An explicit date list is different: its last date is
	// right there in the array, so the bound is applied to that instead. Being
	// precise matters more here than it does for rules -- date lists are finite
	// by nature, and a blanket exemption would pin every finished series to the
	// top of the list forever.
	rows, err := pool.Query(ctx, `
		SELECT `+eventColumns+`
		FROM app_events
		WHERE tenant_id = $1
		  AND ($2::text IS NULL OR status = $2)
		  AND ($3::text IS NULL OR visibility = $3)
		  AND ($4::timestamptz IS NULL
		       OR rrule IS NOT NULL
		       OR greatest(
		            coalesce(ends_at, starts_at),
		            (SELECT max(d) FROM unnest(rdates) AS d)
		          ) >= $4)
		  AND ($5::timestamptz IS NULL OR starts_at < $5)
		  AND (NOT $6::bool OR status <> 'cancelled')
		ORDER BY starts_at ASC
		LIMIT $7`,
		tenantID, nilIfEmpty(string(f.Status)), nilIfEmpty(string(f.Visibility)),
		f.From, f.To, f.ExcludeCancelled, limit,
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

// deleteEventRow destroys a row. Only Service.Delete may call it, and only
// after checking the row is not still holding a pending calendar deletion --
// see the note there.
func deleteEventRow(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID) error {
	tag, err := pool.Exec(ctx, `DELETE FROM app_events WHERE tenant_id = $1 AND id = $2`, tenantID, id)
	if err != nil {
		return fmt.Errorf("deleting event: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
