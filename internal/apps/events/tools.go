package events

import "github.com/mrdon/kit/internal/services"

// eventsTools is the single source of tool metadata. Both the agent registry
// and the MCP server build their surfaces from this slice, so a field added
// here appears on both by construction rather than by remembering to edit two
// switch statements.
//
// Descriptions carry the classification model explicitly, because the axes are
// the part a caller is most likely to get wrong: "published" reads like
// "public" unless told otherwise.
var eventsTools = []services.ToolMeta{
	{
		Name: "create_event",
		Description: "Create an event as a draft. Drafts appear nowhere — not on the calendar, not on the website — " +
			"so an event can be revised freely before anyone sees it. Call publish_event when it is confirmed. " +
			"visibility defaults to private; set it to public only for events the outside world should see.",
		Schema: services.PropsReq(map[string]any{
			"title":       services.Field("string", "Event name, e.g. 'Trivia Night'"),
			"starts_at":   services.Field("string", "Start, e.g. '2026-08-14 19:00' or RFC 3339"),
			"ends_at":     services.Field("string", "End. Defaults to one hour after the start."),
			"timezone":    services.Field("string", "IANA zone such as America/Denver. Defaults to the venue's."),
			"all_day":     services.Field("boolean", "True for an all-day event. Cannot be combined with repeats."),
			"summary":     services.Field("string", "One-line teaser for listings."),
			"description": services.Field("string", "Public description (markdown)."),
			"prep_notes":  services.Field("string", "Internal brief for staff. Goes on the calendar, never on the website."),
			"location":    services.Field("string", "Where, if not the main room, e.g. 'Back room'."),
			"visibility":  services.Field("string", "'public' (website + feed) or 'private' (internal only). Default private."),
			"venue":       services.Field("string", "'onsite' at our venue, or 'offsite' for a festival we attend. Default onsite."),
			"space_impact": services.Field("string",
				"'none' or 'partial' if it reserves an area. Onsite only."),
			"repeat_rule": services.Field("string",
				"Weekly repeat, e.g. 'FREQ=WEEKLY;BYDAY=TU'. Only weekly repeats are supported. "+
					"The start date's weekday must be included in the rule."),
			"price_cents":         services.Field("integer", "Ticket price in cents. Omit for a free event."),
			"currency":            services.Field("string", "ISO currency code. Defaults to USD."),
			"capacity":            services.Field("integer", "Hard seat limit, if any."),
			"expected_attendance": services.Field("integer", "Rough headcount — what the food partner plans around."),
			"registration_url":    services.Field("string", "Where to buy or RSVP, if anywhere."),
			"notify_food_partner": services.Field("boolean",
				"Whether the food partner should plan for this. Defaults true for public onsite events."),
		}, "title", "starts_at"),
	},
	{
		Name: "update_event",
		Description: "Change an event. Only the fields you pass are altered. " +
			"The web address is fixed once an event is published, because links to it may already be shared.",
		Schema: services.PropsReq(map[string]any{
			"event_id":            services.Field("string", "Event id."),
			"title":               services.Field("string", "New title."),
			"starts_at":           services.Field("string", "New start."),
			"ends_at":             services.Field("string", "New end. Pass an empty string to clear it."),
			"timezone":            services.Field("string", "New IANA zone. The wall-clock time is preserved."),
			"all_day":             services.Field("boolean", "All-day flag."),
			"summary":             services.Field("string", "New teaser."),
			"description":         services.Field("string", "New public description."),
			"prep_notes":          services.Field("string", "New internal staff brief."),
			"location":            services.Field("string", "New location."),
			"visibility":          services.Field("string", "'public' or 'private'."),
			"venue":               services.Field("string", "'onsite' or 'offsite'."),
			"space_impact":        services.Field("string", "'none' or 'partial'."),
			"repeat_rule":         services.Field("string", "Weekly repeat rule, or empty to stop repeating."),
			"price_cents":         services.Field("integer", "New price in cents."),
			"clear_price":         services.Field("boolean", "Make the event free."),
			"capacity":            services.Field("integer", "New capacity."),
			"clear_capacity":      services.Field("boolean", "Remove the capacity limit."),
			"expected_attendance": services.Field("integer", "New expected headcount."),
			"registration_url":    services.Field("string", "New ticket/RSVP link."),
			"notify_food_partner": services.Field("boolean", "Whether the food partner should plan for this."),
			"slug":                services.Field("string", "Web address segment. Draft events only."),
		}, "event_id"),
	},
	{
		Name: "publish_event",
		Description: "Mark an event confirmed. This puts it on the team calendar. It reaches the public website " +
			"only if its visibility is also public — publishing a private booking keeps it private.",
		Schema: services.PropsReq(map[string]any{
			"event_id": services.Field("string", "Event id."),
		}, "event_id"),
	},
	{
		Name:        "unpublish_event",
		Description: "Return a published event to draft, removing it from the calendar and the website while it is reworked.",
		Schema: services.PropsReq(map[string]any{
			"event_id": services.Field("string", "Event id."),
		}, "event_id"),
	},
	{
		Name: "cancel_event",
		Description: "Mark an event called off and remove it from the calendar and website. " +
			"This is how an event is deleted; the record is kept so the calendar copy can be cleaned up and the web address is never reused.",
		Schema: services.PropsReq(map[string]any{
			"event_id": services.Field("string", "Event id."),
		}, "event_id"),
	},
	{
		Name: "delete_event",
		Description: "Permanently erase an event and its record. Only possible once the event is cancelled (or still a draft) " +
			"and its calendar entry has already been removed by a sync — otherwise the calendar copy would be left behind. " +
			"Use cancel_event to call an event off; this is for tidying away rows that are no longer needed.",
		Schema: services.PropsReq(map[string]any{
			"event_id": services.Field("string", "Event id."),
		}, "event_id"),
	},
	{
		Name: "events_site_status",
		Description: "Report what has changed since the website was last rebuilt, and when that was. " +
			"The website is a static site, so events edited in Kit are not visible on the web until it is rebuilt.",
		Schema: services.Props(map[string]any{}),
	},
	{
		Name: "events_publish_site",
		Description: "Rebuild the public website so it picks up the latest events. " +
			"Only needed when changes are pending; check events_site_status first.",
		Schema: services.Props(map[string]any{}),
	},
	{
		Name:        "reopen_event",
		Description: "Restore a cancelled event to draft, keeping its original web address.",
		Schema: services.PropsReq(map[string]any{
			"event_id": services.Field("string", "Event id."),
		}, "event_id"),
	},
	{
		Name: "list_events",
		Description: "List events, soonest first. Weekly repeating events are always included regardless of date range, " +
			"because their stored start is the first occurrence and may be long past.",
		Schema: services.Props(map[string]any{
			"status":     services.Field("string", "Filter: draft, published, or cancelled."),
			"visibility": services.Field("string", "Filter: public or private."),
			"from":       services.Field("string", "Only events ending on/after this date."),
			"to":         services.Field("string", "Only events starting before this date."),
			"limit":      services.Field("integer", "Maximum rows (default 200)."),
		}),
	},
	{
		Name:        "get_event",
		Description: "Show one event in full, including its upcoming occurrences if it repeats.",
		Schema: services.Props(map[string]any{
			"event_id": services.Field("string", "Event id."),
			"slug":     services.Field("string", "Web address segment, as an alternative to the id."),
		}),
	},
	{
		Name:        "events_status",
		Description: "Show the events app's configuration and recent calendar sync activity.",
		Schema:      services.Props(map[string]any{}),
		AdminOnly:   true,
	},
	{
		Name:        "events_sync_now",
		Description: "Push pending event changes to Google Calendar immediately instead of waiting for the next scheduled sync.",
		Schema:      services.Props(map[string]any{}),
		AdminOnly:   true,
	},
	{
		Name: "events_reconcile",
		Description: "Compare the calendar against Kit and repair drift — restoring entries deleted directly in Google " +
			"and removing ones that should no longer be there. Pass dry_run first to see exactly what would change; " +
			"this is the only operation that deletes calendar entries. Only entries this app created are ever touched.",
		Schema: services.Props(map[string]any{
			"dry_run": services.Field("boolean", "Preview the changes without applying them."),
		}),
		AdminOnly: true,
	},
}
