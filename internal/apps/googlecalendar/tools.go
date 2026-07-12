package googlecalendar

import "github.com/mrdon/kit/internal/services"

// googleCalendarTools is the shared ToolMeta list. v1 ships one admin
// connection-check tool; the shift sync consumes LoadClient directly.
var googleCalendarTools = []services.ToolMeta{
	{
		Name:        "gcal_check",
		Description: "Verify Kit can write to the connected Google Calendar by writing and immediately deleting a probe event. Use after connecting the service account to confirm the calendar was shared with it correctly.",
		AdminOnly:   true,
		Schema:      services.Props(map[string]any{}),
	},
}
