package square

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mrdon/kit/internal/services"
)

// squareTools is the shared ToolMeta list for the agent + MCP surfaces.
// v1 ships one read-only verification tool; the calendar sync consumes the
// same ListPublishedShifts primitive directly rather than via a tool.
var squareTools = []services.ToolMeta{
	{
		Name:        "square_list_shifts",
		Description: "List published Square scheduled shifts in a date range (read-only). Verifies the Square connection and shows who is scheduled when. Dates are YYYY-MM-DD in your timezone; defaults to the next 14 days.",
		AdminOnly:   true,
		Schema: services.Props(map[string]any{
			"start": services.Field("string", "Start date (YYYY-MM-DD), inclusive. Defaults to today."),
			"end":   services.Field("string", "End date (YYYY-MM-DD), exclusive. Defaults to 14 days after start."),
		}),
	},
}

// resolveRange turns optional YYYY-MM-DD start/end strings into a concrete
// [start, end) window in the caller's timezone. Empty start → today; empty
// end → start + 14 days.
func resolveRange(tz, startStr, endStr string) (time.Time, time.Time, error) {
	loc, err := time.LoadLocation(tz)
	if err != nil || loc == nil {
		loc = time.UTC
	}
	now := time.Now().In(loc)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	if strings.TrimSpace(startStr) != "" {
		if start, err = time.ParseInLocation("2006-01-02", strings.TrimSpace(startStr), loc); err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid start date %q (want YYYY-MM-DD)", startStr)
		}
	}
	end := start.AddDate(0, 0, 14)
	if strings.TrimSpace(endStr) != "" {
		if end, err = time.ParseInLocation("2006-01-02", strings.TrimSpace(endStr), loc); err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid end date %q (want YYYY-MM-DD)", endStr)
		}
	}
	if !end.After(start) {
		return time.Time{}, time.Time{}, errors.New("end date must be after start date")
	}
	return start, end, nil
}

// formatShifts renders enriched shifts as human-readable text. Shared by
// both the agent and MCP handlers so the two surfaces produce identical
// output (per the agent/MCP parity rule).
func formatShifts(shifts []EnrichedShift) string {
	if len(shifts) == 0 {
		return "No published shifts in that range."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d published shift(s):\n\n", len(shifts))
	for _, s := range shifts {
		when := formatShiftTime(s.StartAt, s.EndAt, s.Timezone)
		fmt.Fprintf(&b, "• %s — %s @ %s\n", when, s.Member, s.Location)
		if s.Notes != "" {
			fmt.Fprintf(&b, "    %s\n", s.Notes)
		}
	}
	return b.String()
}

// formatShiftTime renders a shift's start/end in its location timezone.
// Falls back to the raw RFC 3339 strings if they don't parse.
func formatShiftTime(startAt, endAt, tz string) string {
	loc, err := time.LoadLocation(tz)
	if err != nil || loc == nil {
		loc = time.UTC
	}
	start, serr := time.Parse(time.RFC3339, startAt)
	end, eerr := time.Parse(time.RFC3339, endAt)
	if serr != nil || eerr != nil {
		return startAt + " – " + endAt
	}
	return fmt.Sprintf("%s %s–%s",
		start.In(loc).Format("Mon 2006-01-02"),
		start.In(loc).Format("15:04"),
		end.In(loc).Format("15:04 MST"))
}
