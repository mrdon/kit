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
	if cadence := describeCadence(e); cadence != "" {
		out += ", " + cadence
	}
	return out + " (" + e.Timezone + ")"
}

// describeCadence renders how often the event happens, as a clause that reads
// on after a date. Empty for a one-off.
//
// Rules and explicit dates are described together because an event may carry
// both, and because "repeats weekly" printed next to a six-date list is the
// kind of half-truth that gets an event staffed on the wrong night.
func describeCadence(e *Event) string {
	var parts []string
	if e.RRule != "" {
		parts = append(parts, describeRule(e.Rule(), e.StartsAt.In(e.Loc())))
	}
	if n := len(e.RDates); n > 0 {
		if len(parts) > 0 {
			parts = append(parts, "plus "+plural(n, "extra date", "extra dates"))
		} else {
			parts = append(parts, "on "+plural(n+1, "set date", "set dates"))
		}
	}
	return strings.Join(parts, ", ")
}

// describeRepeat is the calendar briefing's sentence about repeats. Same facts
// as describeCadence, punctuated as a standalone line for the staff brief.
func describeRepeat(e *Event) string {
	if c := describeCadence(e); c != "" {
		return "Repeats " + c + "."
	}
	return ""
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// describeRule turns a stored RRULE back into something a human reads without
// knowing RFC 5545.
func describeRule(r *Rule, start time.Time) string {
	if r == nil {
		return "repeating"
	}
	var out string
	if r.Freq == FreqMonthly {
		out = describeMonthly(r, start)
	} else {
		out = describeWeekly(r, start)
	}
	switch {
	case r.Count > 0:
		out += fmt.Sprintf(", %d times", r.Count)
	case !r.Until.IsZero():
		out += ", until " + r.Until.Format("2 Jan 2006")
	}
	return out
}

func describeWeekly(r *Rule, start time.Time) string {
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
	return cadence + strings.Join(names, " and ")
}

func describeMonthly(r *Rule, start time.Time) string {
	every := "every month on "
	if r.Interval > 1 {
		every = fmt.Sprintf("every %d months on ", r.Interval)
	}

	var which []string
	switch {
	case len(r.OrdDays) > 0:
		for _, od := range r.OrdDays {
			if od.Ord == 0 {
				which = append(which, "every "+od.Day.String())
				continue
			}
			which = append(which, "the "+ordinalWord(od.Ord)+" "+od.Day.String())
		}
	case len(r.MonthDays) > 0:
		for _, d := range r.MonthDays {
			which = append(which, "the "+monthDayWord(d))
		}
	default:
		which = append(which, "the "+monthDayWord(start.Day()))
	}
	return every + strings.Join(which, " and ")
}

// ordinalWord names a BYDAY ordinal the way a person would say it.
func ordinalWord(n int) string {
	switch n {
	case 1:
		return "first"
	case 2:
		return "second"
	case 3:
		return "third"
	case 4:
		return "fourth"
	case 5:
		return "fifth"
	case -1:
		return "last"
	case -2:
		return "second-to-last"
	default:
		return fmt.Sprintf("%dth", n)
	}
}

// monthDayWord names a BYMONTHDAY value. Negatives count back from the end,
// where "the last day" beats "the -1st".
func monthDayWord(d int) string {
	switch {
	case d == -1:
		return "last day"
	case d < 0:
		return ordinalWord(-d) + "-to-last day"
	default:
		return ordinalSuffix(d)
	}
}

// ordinalSuffix renders 1 as "1st", 22 as "22nd", 13 as "13th".
func ordinalSuffix(d int) string {
	suffix := "th"
	if d%100 < 11 || d%100 > 13 {
		switch d % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return fmt.Sprintf("%d%s", d, suffix)
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
	if !e.Repeats() {
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

// FormatSiteStatus renders website publish state for the agent and MCP
// surfaces. Lives here, beside the other formatters, so the console and the
// chat surfaces describe the same state in the same words.
func FormatSiteStatus(st SiteStatus) string {
	var b strings.Builder

	if st.BuiltAt == nil {
		b.WriteString("The website has not been rebuilt from Kit yet.\n")
	} else {
		fmt.Fprintf(&b, "Website last rebuilt %s", st.BuiltAt.Format("2 Jan 2006 15:04 MST"))
		if st.BuiltBy != "" {
			fmt.Fprintf(&b, " (%s)", st.BuiltBy)
		}
		b.WriteString(".\n")
	}
	if !st.HookConfigured {
		b.WriteString("No build hook is set, so Kit cannot trigger a rebuild yet.\n")
	}

	if len(st.Pending) == 0 {
		b.WriteString("\nNothing is waiting: the website matches Kit.")
		return b.String()
	}

	n := len(st.Pending)
	word := "changes"
	if n == 1 {
		word = "change"
	}
	fmt.Fprintf(&b, "\n%d %s waiting to go out:\n", n, word)
	for _, c := range st.Pending {
		fmt.Fprintf(&b, "  • %s %s — %s\n", c.Title, c.Verb(), c.CreatedAt.Format("2 Jan 15:04"))
	}
	if st.PendingTruncated {
		b.WriteString("  … and more.\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// FormatNoticePreview renders a shift-notice dry run. Shared by the console,
// agent and MCP surfaces so all three describe the same plan identically.
//
// The full message body is shown, not a count: these DMs carry private-booking
// details to named people, and "3 notices would be sent" gives an admin no way
// to notice the mapping put Saturday's brief in the wrong person's inbox.
func FormatNoticePreview(plans []NoticePlan) string {
	if len(plans) == 0 {
		return "Nobody would be notified: either nobody is on the schedule today, nothing is on, or the people working are not mapped to a Slack user yet."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s would be sent. Nothing has been delivered yet.\n", plural(len(plans), "notice", "notices"))
	for _, p := range plans {
		fmt.Fprintf(&b, "\n--- to %s ---\n%s\n", p.Name, p.Body)
	}
	return b.String()
}
