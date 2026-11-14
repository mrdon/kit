package calendar

import (
	"strings"
	"testing"
	"time"
)

// get_calendar_events is the only tool that returns an event's description,
// so it must return all of it. It used to cut at 200 bytes.
func TestFormatEventsRendersFullDescription(t *testing.T) {
	long := strings.Repeat("Load-in through the side door. ", 12) + "END-OF-DESCRIPTION"
	events := []Event{{
		Summary:     "Private party",
		Description: long + "\nSecond line.",
		StartTime:   time.Date(2026, 12, 11, 23, 0, 0, 0, time.UTC),
	}}

	got := formatEvents(events, time.UTC)

	if !strings.Contains(got, "END-OF-DESCRIPTION") {
		t.Errorf("description was truncated:\n%s", got)
	}
	if !strings.Contains(got, "END-OF-DESCRIPTION\n    Second line.\n") {
		t.Errorf("continuation lines should stay indented under the event:\n%s", got)
	}
}
