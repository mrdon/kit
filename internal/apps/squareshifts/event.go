package squareshifts

import (
	"crypto/sha1" //nolint:gosec // stable content digest, not security
	"encoding/hex"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/googlecalendar"
	"github.com/mrdon/kit/internal/apps/square"
)

// buildEvent maps an enriched Square shift to an all-day Google Calendar
// event with a deterministic id and an idempotency/audit stamp in private
// properties. Shifts render as an all-day banner on the shift's start date
// (matching how teams typically consume a "who's on" schedule), not a timed
// block.
func buildEvent(shift square.EnrichedShift, tenantID uuid.UUID) *googlecalendar.Event {
	// First name keeps the schedule informal; fall back to full name.
	summary := shift.MemberFirst
	if summary == "" {
		summary = shift.Member
	}
	if summary == "" {
		summary = "Shift"
	}
	// All-day events lose the shift hours, so append them to the title —
	// that's what tells opener from closer when several people work a day.
	if clock := shiftClock(shift.StartAt, shift.EndAt); clock != "" {
		summary = summary + " · " + clock
	}
	desc := "Synced from Square."
	if shift.Notes != "" {
		desc = shift.Notes + "\n\n" + desc
	}
	startDate, endDate := shiftDates(shift.StartAt)
	// Ownership stamp (kitApp + kitTenantId) is what the reconcile sweep
	// filters on, so it only ever considers events this app wrote for this
	// tenant. squareShiftId/source are for humans debugging the calendar.
	props := googlecalendar.OwnerProps(AppName, tenantID)
	props["squareShiftId"] = shift.ShiftID
	props["source"] = "square"
	return &googlecalendar.Event{
		ID:                 googlecalendar.DeterministicID("square:" + shift.ShiftID),
		Summary:            summary,
		Location:           shift.Location,
		Description:        desc,
		Start:              &googlecalendar.EventDateTime{Date: startDate},
		End:                &googlecalendar.EventDateTime{Date: endDate},
		ExtendedProperties: &googlecalendar.ExtendedProperties{Private: props},
	}
}

// shiftClock renders a shift's local hours as "6:00am–2:00pm" for the
// all-day event title. RFC 3339 start/end carry their local offset, so
// formatting them directly yields the wall-clock time. Returns "" if either
// timestamp doesn't parse.
func shiftClock(startAt, endAt string) string {
	s, serr := time.Parse(time.RFC3339, startAt)
	e, eerr := time.Parse(time.RFC3339, endAt)
	if serr != nil || eerr != nil {
		return ""
	}
	return s.Format("3:04pm") + "–" + e.Format("3:04pm")
}

// shiftDates returns the all-day start date and (exclusive) end date for a
// shift. The date is taken from the RFC 3339 start's local offset (its first
// 10 chars), so it's already in the shift's local day; end is the next day.
func shiftDates(startAt string) (start, end string) {
	start = startAt
	if len(start) >= 10 {
		start = start[:10]
	}
	end = start
	if d, err := time.Parse("2006-01-02", start); err == nil {
		end = d.AddDate(0, 0, 1).Format("2006-01-02")
	}
	return start, end
}

// contentHash is a stable digest of the event's user-visible fields. An
// unchanged hash means the Google event is already current, so the sync
// skips the write.
func contentHash(e *googlecalendar.Event) string {
	parts := []string{e.Summary, e.Location, e.Description}
	if e.Start != nil {
		parts = append(parts, e.Start.Date, e.Start.DateTime, e.Start.TimeZone)
	}
	if e.End != nil {
		parts = append(parts, e.End.Date, e.End.DateTime, e.End.TimeZone)
	}
	sum := sha1.Sum([]byte(strings.Join(parts, "|"))) //nolint:gosec // not security
	return hex.EncodeToString(sum[:])
}
