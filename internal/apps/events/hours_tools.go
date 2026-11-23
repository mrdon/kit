package events

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mrdon/kit/internal/services"
)

// Holiday hours tools. Appended to eventsTools so the agent and MCP surfaces
// both pick them up from the one slice, like every other events tool.
func init() {
	eventsTools = append(eventsTools, hoursTools...)
}

var hoursTools = []services.ToolMeta{
	{
		Name: "set_holiday_hours",
		Description: "Set the taproom's hours for one date that differs from the usual week — Christmas Eve 12–7, closed on Christmas. " +
			"Replaces whatever that date had. The website shows them, read from the events feed. " +
			"A CLOSED day automatically skips every onsite repeating event on it (happy hour, trivia); one-off events on it are reported to cancel or move. " +
			"On a SHORT day nothing is skipped automatically, but every event running outside the hours is reported so you can skip it with update_event skip_dates. " +
			"Safe to run again: it re-applies the day to events added since.",
		Schema: services.PropsReq(map[string]any{
			"date":   services.Field("string", "The date, YYYY-MM-DD."),
			"label":  services.Field("string", "What the day is, e.g. 'Christmas Eve'."),
			"closed": services.Field("boolean", "True when the taproom is shut all day."),
			"opens":  services.Field("string", "Opening time, 24-hour HH:MM, e.g. '12:00'. Required unless closed."),
			"closes": services.Field("string", "Closing time, 24-hour HH:MM, e.g. '19:00'. Required unless closed."),
			"note":   services.Field("string", "A few public words under the date, e.g. 'No trivia tonight'."),
		}, "date"),
	},
	{
		Name: "remove_holiday_hours",
		Description: "Remove one date's holiday hours, so the usual weekly hours apply again. " +
			"Events a closure skipped stay skipped; un-skip them with update_event skip_dates if they are back on.",
		Schema: services.PropsReq(map[string]any{
			"date": services.Field("string", "The date, YYYY-MM-DD."),
		}, "date"),
	},
	{
		Name:        "list_holiday_hours",
		Description: "List the holiday hours set from today on.",
		Schema:      services.Props(map[string]any{}),
	},
}

// dispatchHours runs the holiday hours tools; ok is false for any other name.
func dispatchHours(ctx context.Context, caller *services.Caller, svc *Service, name string, raw json.RawMessage) (string, bool, error) {
	switch name {
	case "set_holiday_hours":
		out, err := coreSetHours(ctx, caller, svc, raw)
		return out, true, err
	case "remove_holiday_hours":
		out, err := coreRemoveHours(ctx, caller, svc, raw)
		return out, true, err
	case "list_holiday_hours":
		hours, err := svc.ListSpecialHours(ctx, caller.TenantID)
		if err != nil {
			return "", true, err
		}
		return FormatSpecialHours(hours), true, nil
	}
	return "", false, nil
}

func parseHoursDate(s string) (time.Time, error) {
	d, err := time.Parse(skipDayLayout, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, invalid("date must be YYYY-MM-DD")
	}
	return d, nil
}

func coreSetHours(ctx context.Context, caller *services.Caller, svc *Service, raw json.RawMessage) (string, error) {
	var in struct {
		Date   string `json:"date"`
		Label  string `json:"label"`
		Closed bool   `json:"closed"`
		Opens  string `json:"opens"`
		Closes string `json:"closes"`
		Note   string `json:"note"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("parsing input: %w", err)
	}
	day, err := parseHoursDate(in.Date)
	if err != nil {
		return userError(err)
	}
	res, err := svc.SetSpecialHours(ctx, caller.TenantID, SpecialHours{
		Day: day, Label: in.Label, Closed: in.Closed, Opens: in.Opens, Closes: in.Closes, Note: in.Note,
	})
	if err != nil {
		return userError(err)
	}
	return FormatHoursResult(res), nil
}

func coreRemoveHours(ctx context.Context, caller *services.Caller, svc *Service, raw json.RawMessage) (string, error) {
	var in struct {
		Date string `json:"date"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("parsing input: %w", err)
	}
	day, err := parseHoursDate(in.Date)
	if err != nil {
		return userError(err)
	}
	found, err := svc.RemoveSpecialHours(ctx, caller.TenantID, day)
	if err != nil {
		return "", err
	}
	if !found {
		return "No holiday hours were set for " + day.Format("Mon 2 Jan 2006") + ".", nil
	}
	return "Removed the holiday hours for " + day.Format("Mon 2 Jan 2006") +
		". Any events it skipped are still skipped.", nil
}

// FormatSpecialHours renders a list of holiday hours, one line per date.
func FormatSpecialHours(hours []SpecialHours) string {
	if len(hours) == 0 {
		return "No holiday hours set from today on."
	}
	var b strings.Builder
	for _, h := range hours {
		b.WriteString(formatHoursLine(h) + "\n")
	}
	return b.String()
}

func formatHoursLine(h SpecialHours) string {
	when := "closed"
	if !h.Closed {
		when = h.Opens + "–" + h.Closes
	}
	line := "- " + h.Day.Format("Mon 2 Jan 2006")
	if h.Label != "" {
		line += " (" + h.Label + ")"
	}
	line += ": " + when
	if h.Note != "" {
		line += " — " + h.Note
	}
	return line
}

// FormatHoursResult reports a set_holiday_hours call and what it did.
func FormatHoursResult(res *HoursResult) string {
	var b strings.Builder
	b.WriteString("Saved.\n" + formatHoursLine(res.Hours) + "\n")
	if len(res.Skipped) > 0 {
		b.WriteString("\nSkipped on this day because it is closed: " + strings.Join(res.Skipped, ", ") + "\n")
	}
	if len(res.Conflicts) > 0 {
		b.WriteString("\nNeeds a decision:\n")
		for _, c := range res.Conflicts {
			b.WriteString("- " + c + "\n")
		}
	}
	b.WriteString("\nThe website picks this up on its next build.")
	return b.String()
}
