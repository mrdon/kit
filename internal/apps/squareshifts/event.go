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
	summary := shift.Member
	if summary == "" {
		summary = "Shift"
	}
	desc := "Synced from Square."
	if shift.Notes != "" {
		desc = shift.Notes + "\n\n" + desc
	}
	startDate, endDate := shiftDates(shift.StartAt)
	return &googlecalendar.Event{
		ID:          googlecalendar.DeterministicID("square:" + shift.ShiftID),
		Summary:     summary,
		Location:    shift.Location,
		Description: desc,
		Start:       &googlecalendar.EventDateTime{Date: startDate},
		End:         &googlecalendar.EventDateTime{Date: endDate},
		ExtendedProperties: &googlecalendar.ExtendedProperties{Private: map[string]string{
			"squareShiftId": shift.ShiftID,
			"source":        "square",
			"kitTenantId":   tenantID.String(),
		}},
	}
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
