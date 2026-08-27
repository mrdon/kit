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
	out.Recurrence = recurrenceLines(e)
	return out
}

// recurrenceLines renders the event's repeats as RFC 5545 content lines.
//
// RRULE and RDATE compose, exactly as they do in Series.Expand -- Google unions
// them the same way we do, which is what keeps the calendar and Kit showing the
// same dates. Order is fixed so contentHash stays stable across syncs.
func recurrenceLines(e *Event) []string {
	var out []string
	if rule := e.Rule(); rule != nil {
		out = append(out, "RRULE:"+rule.String())
	}
	if len(e.RDates) > 0 {
		out = append(out, rdateLine(e.RDates, e.Loc(), e.Timezone))
	}
	return out
}

// rdateLine renders explicit dates as a single RDATE line.
//
// TZID carries the NAMED zone for the same reason start/end do: without it
// Google reads the values as floating local time in the calendar's own default
// zone, so every date after a DST transition lands an hour out. The values are
// therefore written as bare local date-times -- appending a "Z" or an offset
// here would contradict the TZID and is rejected by RFC 5545.
func rdateLine(dates []time.Time, loc *time.Location, tz string) string {
	stamps := make([]string, len(dates))
	for i, d := range dates {
		stamps[i] = d.In(loc).Format("20060102T150405")
	}
	return "RDATE;TZID=" + tz + ":" + strings.Join(stamps, ",")
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
// This is the bartender's briefing. Whoever is on shift opens the calendar to
// see their shift anyway, so everything they need to run the night has to be
// here and readable on a phone: where it happens, how much room to hold, how
// many people to expect, whether money changes hands.
//
// The operational facts go FIRST, as a scannable block. The marketing blurb is
// for customers and can wait -- burying "reserve the back room for 30" under
// two paragraphs of copy is how it gets missed.
//
// prep_notes belongs here too: the calendar is an internal and partner
// surface. It must never reach the public feed -- see feed.go, which builds
// its payload from a different function for exactly this reason.
func buildDescription(e *Event) string {
	var b strings.Builder
	for _, line := range briefingLines(e) {
		b.WriteString(line + "\n")
	}
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	if s := strings.TrimSpace(e.Summary); s != "" {
		b.WriteString(s + "\n\n")
	}
	if d := strings.TrimSpace(e.Description); d != "" {
		b.WriteString(d + "\n\n")
	}
	if n := strings.TrimSpace(e.PrepNotes); n != "" {
		b.WriteString("Staff notes:\n" + n + "\n\n")
	}
	b.WriteString("Managed by Kit — edits made here are overwritten on the next sync.")
	return b.String()
}

// briefingLines is the operational block: the things someone working the shift
// has to act on, one per line, most physical first.
func briefingLines(e *Event) []string {
	var out []string
	add := func(format string, args ...any) {
		out = append(out, fmt.Sprintf(format, args...))
	}

	if loc := strings.TrimSpace(e.Location); loc != "" {
		add("Where: %s", loc)
	}
	if e.Venue == VenueOffsite {
		add("Offsite — not at the taproom.")
	}

	// Space and headcount answer the same practical question ("how much room
	// do I hold, and for how many?"), so they sit together.
	switch {
	case e.SpaceImpact == SpaceImpactPartial && e.ExpectedAttendance != nil:
		add("Reserve: part of the room, for ~%d people.", *e.ExpectedAttendance)
	case e.SpaceImpact == SpaceImpactPartial:
		add("Reserve: part of the room.")
	case e.ExpectedAttendance != nil:
		add("Expect: ~%d people. Room stays open as usual.", *e.ExpectedAttendance)
	}
	if e.Capacity != nil {
		add("Places: %d.", *e.Capacity)
	}
	if p := formatPrice(e); p != "" {
		add("Cost: %s.", p)
	}
	if u := strings.TrimSpace(e.RegistrationURL); u != "" {
		add("Tickets: %s", u)
	}
	if repeat := describeRepeat(e); repeat != "" {
		add("%s", repeat)
	}
	return out
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
