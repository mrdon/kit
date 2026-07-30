package events

import (
	"fmt"
	"strings"
	"time"
)

// All user-visible rendering lives here so the agent and MCP surfaces produce
// identical text. A formatter inlined into one handler is how the two drift.

// FormatEvent renders one event for a chat or MCP response.
func FormatEvent(e *Event, s Settings) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", e.Title)
	fmt.Fprintf(&b, "  id: %s\n", e.ID)
	fmt.Fprintf(&b, "  when: %s\n", formatWhen(e))
	fmt.Fprintf(&b, "  status: %s (%s)\n", e.Status, describeExposure(e))

	if e.Location != "" {
		fmt.Fprintf(&b, "  where: %s\n", e.Location)
	}
	if e.Venue == VenueOffsite {
		b.WriteString("  offsite event\n")
	}
	if e.SpaceImpact == SpaceImpactPartial {
		b.WriteString("  reserves part of the room\n")
	}
	if price := formatPrice(e); price != "" {
		fmt.Fprintf(&b, "  price: %s\n", price)
	}
	if e.Capacity != nil {
		fmt.Fprintf(&b, "  capacity: %d\n", *e.Capacity)
	}
	if e.ExpectedAttendance != nil {
		fmt.Fprintf(&b, "  expected: ~%d people\n", *e.ExpectedAttendance)
	}
	if e.RegistrationURL != "" {
		fmt.Fprintf(&b, "  tickets: %s\n", e.RegistrationURL)
	}
	if url := s.CanonicalURL(e.Slug); url != "" && e.IsPubliclyVisible() {
		fmt.Fprintf(&b, "  page: %s\n", url)
	}
	if e.NotifyFoodPartner {
		b.WriteString("  food partner should plan for this\n")
	}
	if e.Summary != "" {
		fmt.Fprintf(&b, "  summary: %s\n", e.Summary)
	}
	if e.PrepNotes != "" {
		fmt.Fprintf(&b, "  staff notes: %s\n", firstLine(e.PrepNotes))
	}
	return b.String()
}

// describeExposure states plainly where an event actually appears. "published"
// is routinely misread as "public", so the two axes are spelled out together.
func describeExposure(e *Event) string {
	switch {
	case e.Status == StatusDraft:
		return "not visible anywhere yet"
	case e.Status == StatusCancelled:
		return "removed from the calendar and website"
	case e.IsPubliclyVisible():
		return "on the calendar and the public website"
	default:
		return "on the team calendar only, not public"
	}
}

func formatWhen(e *Event) string {
	loc := e.Loc()
	start := e.StartsAt.In(loc)
	layout := "Mon 2 Jan 2006 3:04pm"
	if e.AllDay {
		layout = "Mon 2 Jan 2006"
	}
	out := start.Format(layout)
	if !e.AllDay && e.EndsAt != nil {
		out += "–" + e.EndsAt.In(loc).Format("3:04pm")
	}
	if e.RRule != "" {
		out += ", " + describeRule(e.Rule(), start)
	}
	return out + " (" + e.Timezone + ")"
}

// describeRule turns a stored RRULE back into something a human reads without
// knowing RFC 5545.
func describeRule(r *Rule, start time.Time) string {
	if r == nil {
		return "repeating"
	}
	days := r.Days
	if len(days) == 0 {
		days = []time.Weekday{start.Weekday()}
	}
	names := make([]string, len(days))
	for i, d := range days {
		names[i] = d.String()
	}
	cadence := "every "
	if r.Interval > 1 {
		cadence = fmt.Sprintf("every %d weeks on ", r.Interval)
	}
	out := cadence + strings.Join(names, " and ")
	switch {
	case r.Count > 0:
		out += fmt.Sprintf(", %d times", r.Count)
	case !r.Until.IsZero():
		out += ", until " + r.Until.Format("2 Jan 2006")
	}
	return out
}

func formatPrice(e *Event) string {
	if e.PriceCents == nil {
		return ""
	}
	if *e.PriceCents == 0 {
		return "free"
	}
	cur := e.Currency
	if cur == "" {
		cur = "USD"
	}
	return fmt.Sprintf("%.2f %s", float64(*e.PriceCents)/100, cur)
}

// FormatEventList renders a listing, one line per event.
func FormatEventList(events []Event, s Settings) string {
	if len(events) == 0 {
		return "No events match."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d event(s):\n", len(events))
	for i := range events {
		e := &events[i]
		fmt.Fprintf(&b, "- %s — %s [%s, %s]\n", e.Title, formatWhen(e), e.Status, e.Visibility)
		fmt.Fprintf(&b, "    id: %s\n", e.ID)
	}
	_ = s
	return b.String()
}

// FormatOccurrences lists the next few dates a repeating event falls on.
func FormatOccurrences(e *Event, limit int) string {
	if e.RRule == "" {
		return ""
	}
	now := time.Now()
	occ := e.Occurrences(now, now.AddDate(1, 0, 0))
	if len(occ) == 0 {
		return "  next: no further occurrences\n"
	}
	if len(occ) > limit {
		occ = occ[:limit]
	}
	loc := e.Loc()
	parts := make([]string, len(occ))
	for i, o := range occ {
		parts[i] = o.Start.In(loc).Format("Mon 2 Jan")
	}
	return "  next: " + strings.Join(parts, ", ") + "\n"
}

// FormatWarnings renders publish-time advice. Empty when there is none.
func FormatWarnings(warnings []string) string {
	if len(warnings) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nWorth knowing:\n")
	for _, w := range warnings {
		fmt.Fprintf(&b, "  - %s\n", w)
	}
	return b.String()
}

// FormatSettings renders the admin configuration summary.
func FormatSettings(s Settings) string {
	var b strings.Builder
	b.WriteString("Events configuration:\n")
	if s.CalendarConfigured() {
		fmt.Fprintf(&b, "  calendar: %s\n", s.CalendarID)
	} else {
		b.WriteString("  calendar: not selected — events will not sync until one is chosen\n")
	}
	fmt.Fprintf(&b, "  timezone: %s\n", firstNonEmpty(s.Timezone, DefaultTimezone))
	if s.PublicURLTemplate != "" {
		fmt.Fprintf(&b, "  website pages: %s\n", s.PublicURLTemplate)
	} else {
		b.WriteString("  website pages: no URL template configured\n")
	}
	if s.FeedToken != "" {
		b.WriteString("  website feed: token configured\n")
	} else {
		b.WriteString("  website feed: no token yet\n")
	}
	return b.String()
}

func firstLine(s string) string {
	if head, _, found := strings.Cut(s, "\n"); found {
		return strings.TrimSpace(head) + "…"
	}
	return strings.TrimSpace(s)
}
