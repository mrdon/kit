package events

import "github.com/mrdon/kit/internal/services"

// dateList describes a repeat-date array. Spelled out rather than built with
// services.Field because that helper emits no "items", and a typed array
// without one is rejected by strict MCP clients.
func dateList(desc string) map[string]any {
	return map[string]any{
		"type":        "array",
		"items":       map[string]any{"type": "string"},
		"description": desc,
	}
}

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
			"prominence":  services.Field("string", "How loudly this speaks: \"featured\" (the website leads with it; several may be marked at once), \"normal\" (a real event, the default), or \"background\" (a standing offer such as a weekly pizza deal or happy hour — it still gets printed and published, but never takes the headline off a real event on the same day)."),
			"summary":     services.Field("string", "One-line teaser for listings."),
			"description": services.Field("string", "Public description (markdown)."),
			"prep_notes":  services.Field("string", "Internal brief for staff. Goes on the calendar, never on the website."),
			"location":    services.Field("string", "Where, if not the main room, e.g. 'Back room'."),
			"visibility":  services.Field("string", "'public' (website + feed) or 'private' (internal only). Default private."),
			"venue":       services.Field("string", "'onsite' at our venue, or 'offsite' for a festival we attend. Default onsite."),
			"space_impact": services.Field("string",
				"'none' or 'partial' if it reserves an area. Onsite only."),
			"repeat_rule": services.Field("string",
				"A repeat rule, for series that follow a pattern. Weekly: 'FREQ=WEEKLY;BYDAY=TU'. "+
					"Monthly: 'FREQ=MONTHLY;BYDAY=1FR' (first Friday), 'FREQ=MONTHLY;BYDAY=-1FR' (last Friday), "+
					"'FREQ=MONTHLY;BYMONTHDAY=15' (the 15th). Add INTERVAL for 'every 2 weeks'/'every 3 months', "+
					"and UNTIL or COUNT to stop it. The start date must itself fall on the pattern. "+
					"For dates that follow no pattern, use repeat_dates instead."),
			"repeat_dates": dateList(
				"Extra dates this same event also happens on, e.g. ['2026-10-02 19:00', '2026-11-06 19:00']. " +
					"Use this for a series with no pattern — dates picked around someone's availability, or a run " +
					"with a gap over a holiday. One event, one web page, many dates; do not create one event per night. " +
					"The earliest date given becomes the start."),
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
			"event_id":     services.Field("string", "Event id."),
			"title":        services.Field("string", "New title."),
			"starts_at":    services.Field("string", "New start."),
			"ends_at":      services.Field("string", "New end. Pass an empty string to clear it."),
			"timezone":     services.Field("string", "New IANA zone. The wall-clock time is preserved."),
			"all_day":      services.Field("boolean", "All-day flag."),
			"prominence":   services.Field("string", "New prominence: \"featured\", \"normal\" or \"background\". Background is for standing offers that must not headline a day that has a real event on it."),
			"summary":      services.Field("string", "New teaser."),
			"description":  services.Field("string", "New public description."),
			"prep_notes":   services.Field("string", "New internal staff brief."),
			"location":     services.Field("string", "New location."),
			"visibility":   services.Field("string", "'public' or 'private'."),
			"venue":        services.Field("string", "'onsite' or 'offsite'."),
			"space_impact": services.Field("string", "'none' or 'partial'."),
			"repeat_rule": services.Field("string",
				"Repeat rule (weekly or monthly, as in create_event), or empty to stop repeating."),
			"repeat_dates": dateList(
				"Replaces the whole list of extra dates. Pass the full list you want, not just additions; " +
					"an empty list turns the event back into a one-off."),
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
		Name: "clone_event",
		Description: "Copy an existing event into a new draft — same blurb, staff notes, price, capacity and poster. " +
			"Use this for 'the same again' rather than retyping it. The copy is independent: editing one never " +
			"changes the other, and it gets its own web address. Give starts_at to put the copy on a new date " +
			"(which drops any extra dates the original had); omit it to duplicate the schedule exactly.",
		Schema: services.PropsReq(map[string]any{
			"event_id":  services.Field("string", "Event to copy."),
			"starts_at": services.Field("string", "Start for the copy, e.g. '2026-09-11 19:00'. Defaults to the original's."),
			"title":     services.Field("string", "Title for the copy. Defaults to the original's with '(copy)' appended."),
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
		Description: "List events, soonest first. Rule-based repeating events are always included regardless of " +
			"date range, because their stored start is the first occurrence and may be long past. An event with a " +
			"list of set dates is included while any of those dates is still ahead.",
		Schema: services.Props(map[string]any{
			"status":     services.Field("string", "Filter: draft, published, or cancelled."),
			"visibility": services.Field("string", "Filter: public or private."),
			"from":       services.Field("string", "Only events ending on/after this date."),
			"to":         services.Field("string", "Only events starting before this date."),
			"limit":      services.Field("integer", "Maximum rows (default 200)."),
		}),
	},
	{
		Name: "event_poster_upload_url",
		Description: "Get a one-time link for putting a poster image on an event. Tool calls carry text, not " +
			"image bytes, so this hands back a URL to POST the file to instead: " +
			"curl -F poster=@poster.jpg '<url>'. JPEG, PNG, WebP or GIF, up to 8MB. The link works once, " +
			"expires after 15 minutes, and sets the poster on that one event — ask for another to replace it. " +
			"create_event already returns one, so this is for later, or for an event that already exists.",
		Schema: services.PropsReq(map[string]any{
			"event_id": services.Field("string", "Event to attach the poster to."),
		}, "event_id"),
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
	{
		Name:        "events_staff_map",
		Description: "Show which Square team members are mapped to which Slack users for shift notices, and — more usefully — who is working but NOT mapped and therefore silently receiving nothing.",
		AdminOnly:   true,
		Schema:      services.Props(map[string]any{}),
	},
	{
		Name: "events_map_staff",
		Description: "Map a Square team member to the Slack user Kit should DM about the events on their shifts. " +
			"Both arguments accept a name or an id. Omit slack_user to clear the mapping and stop that person's notices. " +
			"The Events staff page in the console does the same thing with two dropdowns, which is easier when you do not know the ids.",
		AdminOnly: true,
		Schema: services.PropsReq(map[string]any{
			"square_team_member": services.Field("string", "The Square team member: their name as it appears on the schedule, or the Square team member id."),
			"slack_user":         services.Field("string", "The Slack person to notify: their display name, or a U… Slack user id. Omit to clear the mapping."),
		}, "square_team_member"),
	},
	{
		Name: "events_shift_notices",
		Description: "Preview or send today's shift notices — the DM each working staff member gets listing what is on today. " +
			"Defaults to a preview showing the exact message text per person; pass send true to deliver. " +
			"Sending twice is safe: a notice already delivered unchanged is not repeated.",
		AdminOnly: true,
		Schema: services.Props(map[string]any{
			"send": services.Field("boolean", "Deliver the notices. Omit or pass false to preview the messages without sending."),
		}),
	},

	// Promotion channels. The console page is the comfortable way to edit one;
	// these exist because SETTING UP is a bulk job -- eight or nine
	// destinations each with a campaign -- and doing that through a form eight
	// times is worse than describing it once.
	{
		Name: "events_channels",
		Description: "List where events get promoted and how much of it is still manual, plus the calendar feed addresses to hand out. " +
			"Use this before adding a channel, to see what already exists.",
		AdminOnly: true,
		Schema:    services.Props(map[string]any{}),
	},
	{
		Name: "events_add_channel",
		Description: "Add a place events get promoted to — a chamber of commerce calendar, a city calendar, a newsletter, a Facebook or Instagram account. " +
			"Pick a campaign rather than describing timings: submit_once for a calendar you fill a form in for, announce_and_remind for a feed post, " +
			"day_of_only for stories (they expire in 24h), every_few_weeks for periodic reminders only. " +
			"Kit works out which steps suit a one-off versus a standing weekly series, so the same channel does the right thing for both.",
		AdminOnly: true,
		Schema: services.PropsReq(map[string]any{
			"name":            services.Field("string", "What to call it, e.g. 'Louisville Chamber of Commerce'."),
			"campaign":        services.Field("string", "submit_once | announce_and_remind | day_of_only | every_few_weeks"),
			"submit_url":      services.Field("string", "Deep link to their submit form, or your account page. Opened straight from the checklist, so the deeper the better."),
			"min_prominence":  services.Field("string", "Lowest prominence this destination wants: 'featured' for a chamber that only takes your big events, 'normal' for real events but not standing offers, 'background' for everything including happy hour. Defaults to normal."),
			"lead_time_days":  services.Field("integer", "How far ahead they need telling. This sets urgency: a calendar wanting 14 days is already late for an event 10 days out. Defaults to 0."),
			"include_offsite": services.Field("boolean", "Whether to send events you are only ATTENDING rather than hosting. Usually false for a community calendar (the organiser already lists it) and true for your own social accounts. Defaults to false."),
			"submit_label":    services.Field("string", "Wording for the checklist row on a submit_once channel, e.g. 'Add to the chamber calendar'."),
		}, "name", "campaign"),
	},
	{
		Name: "events_update_channel",
		Description: "Change a promotion channel. The important one is mode: set it to 'subscribed' once a calendar agrees to pull your ICS feed and it stops generating work entirely. " +
			"Also pass verified true when you have actually seen your events appear on their calendar — a subscribed channel produces no reminders, so an unconfirmed one fails silently. " +
			"Set active false to stop the reminders while keeping the record.",
		AdminOnly: true,
		Schema: services.PropsReq(map[string]any{
			"channel":         services.Field("string", "Which channel, by name or id."),
			"name":            services.Field("string", "Rename it."),
			"mode":            services.Field("string", "manual (you do it) | subscribed (they pull the feed, no work) | automated (Kit posts it, needs a connector)."),
			"feed_tier":       services.Field("string", "For a subscribed channel, which feed they pull: all | highlights | featured."),
			"verified":        services.Field("boolean", "Pass true to record that you have confirmed a subscribed channel really is pulling the feed."),
			"submit_url":      services.Field("string", "Deep link to their submit form."),
			"min_prominence":  services.Field("string", "background | normal | featured."),
			"lead_time_days":  services.Field("integer", "How far ahead they need telling."),
			"include_offsite": services.Field("boolean", "Whether to send events you are only attending."),
			"active":          services.Field("boolean", "False stops the reminders but keeps the history."),
			"campaign":        services.Field("string", "Replace the campaign: submit_once | announce_and_remind | day_of_only | every_few_weeks."),
			"submit_label":    services.Field("string", "Wording for the checklist row on a submit_once channel."),
		}, "channel"),
	},
	{
		Name: "events_remove_channel",
		Description: "Delete a promotion channel and its record of what was already posted there. " +
			"To stop the reminders but keep the history, use events_update_channel with active false instead.",
		AdminOnly: true,
		Schema: services.PropsReq(map[string]any{
			"channel": services.Field("string", "Which channel, by name or id."),
		}, "channel"),
	},
	{
		Name: "events_promo_list",
		Description: "Show what still needs posting where, in the order it wants doing. " +
			"Ordered by each destination's own deadline rather than the event date, so a calendar wanting a fortnight's notice comes first. " +
			"Reminders whose window has closed are already gone rather than piling up.",
		Schema: services.Props(map[string]any{}),
	},
}
