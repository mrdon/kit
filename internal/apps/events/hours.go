package events

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Holiday hours: the dates the taproom opens differently from the usual week,
// or not at all. See migration 136.
//
// They override events in one direction only. A CLOSED day skips every onsite
// repeating event that lands on it, through the same skip list a person would
// edit, so the calendar, the website, the wall screens and the ICS feed all
// drop it with no special case of their own. A SHORT day does not skip
// anything: an event running past close is sometimes the point (the Monday
// game runs to 9:20 on a 9pm close), so clashes are reported for a person to
// decide rather than silently removed.

// SpecialHours is one date's hours.
type SpecialHours struct {
	Day    time.Time `json:"-"`
	Label  string    `json:"label,omitempty"`
	Closed bool      `json:"closed"`
	Opens  string    `json:"opens,omitempty"`
	Closes string    `json:"closes,omitempty"`
	Note   string    `json:"note,omitempty"`
}

// DayString is the date as YYYY-MM-DD.
func (h SpecialHours) DayString() string { return h.Day.Format(skipDayLayout) }

// HoursResult reports what setting a day's hours did to the events on it.
type HoursResult struct {
	Hours SpecialHours
	// Skipped are repeating events now skipped because the day is closed.
	Skipped []string
	// Conflicts need a person: a one-off on a closed day, or an event that
	// runs outside a short day's hours.
	Conflicts []string
}

// clockLayout is the HH:MM wall-clock form opens/closes are stored in.
const clockLayout = "15:04"

// normaliseHours validates and tidies a day's hours before saving.
func normaliseHours(h *SpecialHours) error {
	h.Label = strings.TrimSpace(h.Label)
	h.Note = strings.TrimSpace(h.Note)
	if h.Closed {
		h.Opens, h.Closes = "", ""
		return nil
	}
	o, err := time.Parse(clockLayout, strings.TrimSpace(h.Opens))
	if err != nil {
		return invalid("opens must be a 24-hour time such as 12:00, or set closed")
	}
	c, err := time.Parse(clockLayout, strings.TrimSpace(h.Closes))
	if err != nil {
		return invalid("closes must be a 24-hour time such as 19:00, or set closed")
	}
	if !c.After(o) {
		return invalid("closes must be after opens")
	}
	h.Opens, h.Closes = o.Format(clockLayout), c.Format(clockLayout)
	return nil
}

func upsertSpecialHours(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, h SpecialHours) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO app_event_special_hours (tenant_id, day, label, closed, opens, closes, note)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (tenant_id, day) DO UPDATE SET
			label = EXCLUDED.label, closed = EXCLUDED.closed, opens = EXCLUDED.opens,
			closes = EXCLUDED.closes, note = EXCLUDED.note, updated_at = now()`,
		tenantID, h.DayString(), h.Label, h.Closed, h.Opens, h.Closes, h.Note)
	if err != nil {
		return fmt.Errorf("saving holiday hours: %w", err)
	}
	return nil
}

// deleteSpecialHours removes one date, reporting whether it existed.
func deleteSpecialHours(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, day time.Time) (bool, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM app_event_special_hours WHERE tenant_id = $1 AND day = $2`,
		tenantID, day.Format(skipDayLayout))
	if err != nil {
		return false, fmt.Errorf("deleting holiday hours: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// listSpecialHours returns the dates on or after from (a YYYY-MM-DD), soonest first.
func listSpecialHours(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, from string) ([]SpecialHours, error) {
	rows, err := pool.Query(ctx, `
		SELECT day, label, closed, opens, closes, note
		FROM app_event_special_hours
		WHERE tenant_id = $1 AND day >= $2
		ORDER BY day`, tenantID, from)
	if err != nil {
		return nil, fmt.Errorf("listing holiday hours: %w", err)
	}
	defer rows.Close()
	var out []SpecialHours
	for rows.Next() {
		var h SpecialHours
		if err := rows.Scan(&h.Day, &h.Label, &h.Closed, &h.Opens, &h.Closes, &h.Note); err != nil {
			return nil, fmt.Errorf("scanning holiday hours: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
