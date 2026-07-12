package squareshifts

import (
	"crypto/sha1" //nolint:gosec // stable content digest, not security
	"encoding/hex"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/googlecalendar"
	"github.com/mrdon/kit/internal/apps/square"
)

// buildEvent maps an enriched Square shift to a Google Calendar event with a
// deterministic id and an idempotency/audit stamp in private properties.
func buildEvent(shift square.EnrichedShift, tenantID uuid.UUID) *googlecalendar.Event {
	summary := shift.Member
	if summary == "" {
		summary = "Shift"
	}
	desc := "Synced from Square."
	if shift.Notes != "" {
		desc = shift.Notes + "\n\n" + desc
	}
	return &googlecalendar.Event{
		ID:          googlecalendar.DeterministicID("square:" + shift.ShiftID),
		Summary:     summary,
		Location:    shift.Location,
		Description: desc,
		Start:       &googlecalendar.EventDateTime{DateTime: shift.StartAt, TimeZone: shift.Timezone},
		End:         &googlecalendar.EventDateTime{DateTime: shift.EndAt, TimeZone: shift.Timezone},
		ExtendedProperties: &googlecalendar.ExtendedProperties{Private: map[string]string{
			"squareShiftId": shift.ShiftID,
			"source":        "square",
			"kitTenantId":   tenantID.String(),
		}},
	}
}

// contentHash is a stable digest of the event's user-visible fields. An
// unchanged hash means the Google event is already current, so the sync
// skips the write.
func contentHash(e *googlecalendar.Event) string {
	parts := []string{e.Summary, e.Location, e.Description}
	if e.Start != nil {
		parts = append(parts, e.Start.DateTime, e.Start.TimeZone)
	}
	if e.End != nil {
		parts = append(parts, e.End.DateTime, e.End.TimeZone)
	}
	sum := sha1.Sum([]byte(strings.Join(parts, "|"))) //nolint:gosec // not security
	return hex.EncodeToString(sum[:])
}
