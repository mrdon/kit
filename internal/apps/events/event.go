package events

import (
	"crypto/sha1" //nolint:gosec // stable content digest, not security
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/googlecalendar"
)

// googleEventID derives the calendar id from the event row. Deterministic, so
// a re-sync patches the same entry instead of creating a duplicate, and so a
// republished event reclaims its original id.
func googleEventID(eventID uuid.UUID) string {
	return googlecalendar.DeterministicID("event:" + eventID.String())
}

// buildEvent maps an event row to a Google Calendar entry.
func buildEvent(e *Event, tenantID uuid.UUID) *googlecalendar.Event {
	props := googlecalendar.OwnerProps(AppName, tenantID)
	props["kitEventId"] = e.ID.String()

	out := &googlecalendar.Event{
		ID:          googleEventID(e.ID),
		Summary:     buildSummary(e),
		Description: buildDescription(e),
		Location:    e.Location,
		// Set explicitly rather than left empty. A cancel deletes the entry,
		// and republishing reuses the same deterministic id -- which Google may
		// still hold as a tombstone, making UpsertEvent fall back to a full
		// PUT. Asserting "confirmed" means the revived event's state is ours
		// rather than whatever Google's reset semantics produce.
		Status:             "confirmed",
		ExtendedProperties: &googlecalendar.ExtendedProperties{Private: props},
	}

	if e.AllDay {
		// All-day events use date-only endpoints, and the end is exclusive.
		loc := e.Loc()
		start := e.StartsAt.In(loc)
		end := e.End().In(loc)
		if !end.After(start) {
			end = start
		}
		out.Start = &googlecalendar.EventDateTime{Date: start.Format("2006-01-02")}
		out.End = &googlecalendar.EventDateTime{Date: end.AddDate(0, 0, 1).Format("2006-01-02")}
		return out
	}

	// Timed events carry the NAMED zone, not a UTC offset. This is required
	// for recurrence: without it Google expands the series in the calendar's
	// own default zone, which drifts an hour at every DST transition. The
	// shift-sync template writes all-day events and never sets TimeZone, so
	// this is the line most easily lost when copying it.
	out.Start = &googlecalendar.EventDateTime{
		DateTime: e.StartsAt.In(e.Loc()).Format(time.RFC3339),
		TimeZone: e.Timezone,
	}
	out.End = &googlecalendar.EventDateTime{
		DateTime: e.End().In(e.Loc()).Format(time.RFC3339),
		TimeZone: e.Timezone,
	}
	if rule := e.Rule(); rule != nil {
		out.Recurrence = []string{"RRULE:" + rule.String()}
	}
	return out
}

// buildSummary prefixes the title by classification.
//
// The calendar is one shared surface read by bar staff, the sales side, and
// the food partner, so the title is the only signal any of them gets about
// what kind of event it is. One helper so the format can be changed in one
// place after real use -- the shift sync took two passes to settle its own.
func buildSummary(e *Event) string {
	title := strings.TrimSpace(e.Title)
	if title == "" {
		title = "Event"
	}
	switch {
	case e.Status == StatusCancelled:
		return "❌ CANCELLED — " + title
	case e.Venue == VenueOffsite:
		return "🚚 Offsite — " + title
	case e.Visibility == VisibilityPrivate:
		return "🔒 Private — " + title
	default:
		return "🍺 " + title
	}
}

// buildDescription assembles the calendar body.
//
// prep_notes belongs here: the calendar is an internal and partner surface,
// and the bartender working the shift is already looking at it. It must never
// reach the public feed -- see feed.go, which builds its payload from a
// different function for exactly this reason.
func buildDescription(e *Event) string {
	var b strings.Builder
	if s := strings.TrimSpace(e.Summary); s != "" {
		b.WriteString(s + "\n\n")
	}
	if d := strings.TrimSpace(e.Description); d != "" {
		b.WriteString(d + "\n\n")
	}
	if n := strings.TrimSpace(e.PrepNotes); n != "" {
		b.WriteString("Staff notes:\n" + n + "\n\n")
	}
	if e.SpaceImpact == SpaceImpactPartial {
		b.WriteString("Reserves part of the room.\n")
	}
	if e.ExpectedAttendance != nil {
		fmt.Fprintf(&b, "Expected: ~%d people.\n", *e.ExpectedAttendance)
	}
	if e.Capacity != nil {
		fmt.Fprintf(&b, "Capacity: %d.\n", *e.Capacity)
	}
	if p := formatPrice(e); p != "" {
		fmt.Fprintf(&b, "Price: %s.\n", p)
	}
	if u := strings.TrimSpace(e.RegistrationURL); u != "" {
		fmt.Fprintf(&b, "Tickets: %s\n", u)
	}
	b.WriteString("\nManaged by Kit — edits made here are overwritten on the next sync.")
	return b.String()
}

// contentHash digests everything a write would send, so an unchanged event is
// skipped instead of re-patched every 15 minutes.
//
// Recurrence, status, and the target calendar are part of the digest
// deliberately. The shift-sync version hashes only summary/location/times; copy
// that verbatim and editing trivia from Tuesdays to Wednesdays produces an
// identical hash, so the sync skips the write and reconcile does not catch it
// either -- reconcile compares presence by id, not content. Google keeps the
// old series indefinitely, with nothing logged.
func contentHash(ev *googlecalendar.Event, calendarID string) string {
	h := sha1.New() //nolint:gosec // stable content digest, not security
	write := func(parts ...string) {
		for _, p := range parts {
			h.Write([]byte(p))
			h.Write([]byte{0})
		}
	}
	write(calendarID, ev.Summary, ev.Description, ev.Location, ev.Status)
	write(strings.Join(ev.Recurrence, "|"))
	if ev.Start != nil {
		write(ev.Start.DateTime, ev.Start.Date, ev.Start.TimeZone)
	}
	if ev.End != nil {
		write(ev.End.DateTime, ev.End.Date, ev.End.TimeZone)
	}
	return hex.EncodeToString(h.Sum(nil))
}
