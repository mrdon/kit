package events

import (
	"slices"
	"strings"
	"time"

	ics "github.com/arran4/golang-ical"
)

// Skip dates: the days a repeating event does NOT happen -- RFC 5545's EXDATE.
//
// Trivia is off on Christmas Eve; the taproom is shut on New Year's Day. Before
// this, the only way to drop one night from a weekly series was to end the
// rule and start a second event, which split one programme across two pages.
//
// A skip is stored as the occurrence's start instant but MATCHED by local
// calendar date in the event's zone. Matching on the instant would un-skip the
// night the moment someone moved trivia from 6:30 to 7pm, and nobody reading
// "skip Dec 24" means "skip Dec 24 only if it still starts at 6:30".

// skipDayLayout is the calendar-date key skips are matched on.
const skipDayLayout = "2006-01-02"

// dropSkipped removes every occurrence whose local start date is skipped.
func dropSkipped(out []Occurrence, skips []time.Time, loc *time.Location) []Occurrence {
	if len(skips) == 0 {
		return out
	}
	days := make(map[string]bool, len(skips))
	for _, s := range skips {
		days[s.In(loc).Format(skipDayLayout)] = true
	}
	return slices.DeleteFunc(out, func(o Occurrence) bool {
		return days[o.Start.In(loc).Format(skipDayLayout)]
	})
}

// resolveSkipDates turns human-entered dates into stored skips.
//
// Each date must land on a day the event actually happens. A skip that names
// a Wednesday for a Thursday series is a typo, and storing it would make the
// caller believe trivia is off when it is not -- so it is refused, naming the
// date, rather than kept as a no-op.
func resolveSkipDates(e *Event, raw []string) ([]time.Time, error) {
	var out []time.Time
	loc := e.Loc()
	base := e.Series()
	base.ExDates = nil
	for _, s := range raw {
		if strings.TrimSpace(s) == "" {
			continue
		}
		t, err := ParseTime(s, loc)
		if err != nil {
			return nil, err
		}
		if !e.Repeats() {
			return nil, invalid("only a repeating event can skip a date; cancel a one-off event instead")
		}
		local := t.In(loc)
		day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
		occ := base.Expand(day, day.AddDate(0, 0, 1))
		if len(occ) == 0 {
			return nil, invalid("%s does not happen on %s, so there is nothing to skip",
				e.Title, day.Format("Mon 2 Jan 2006"))
		}
		out = append(out, occ[0].Start)
	}
	if len(out) > maxRDates {
		return nil, invalid("an event can skip at most %d dates", maxRDates)
	}
	slices.SortFunc(out, func(a, b time.Time) int { return a.Compare(b) })
	return slices.CompactFunc(out, func(a, b time.Time) bool { return a.Equal(b) }), nil
}

// exdateStamps renders the skips for a calendar, re-timed to the event's
// CURRENT start time. EXDATE must equal the occurrence it removes exactly, so
// a skip stored at the old 6:30 would silently stop matching after a move to
// 7pm. All-day events take bare dates.
func exdateStamps(e *Event) []string {
	loc := e.Loc()
	start := e.StartsAt.In(loc)
	out := make([]string, 0, len(e.ExDates))
	for _, d := range e.ExDates {
		l := d.In(loc)
		if e.AllDay {
			out = append(out, l.Format("20060102"))
			continue
		}
		at := time.Date(l.Year(), l.Month(), l.Day(), start.Hour(), start.Minute(), start.Second(), 0, loc)
		out = append(out, at.Format(icsLocalLayout))
	}
	slices.Sort(out)
	return out
}

// exdateLine is the Google Calendar recurrence line for the skips, or "" when
// there are none. TZID carries the named zone for the same reason rdateLine's
// does.
func exdateLine(e *Event) string {
	if len(e.ExDates) == 0 {
		return ""
	}
	stamps := strings.Join(exdateStamps(e), ",")
	if e.AllDay {
		return "EXDATE;VALUE=DATE:" + stamps
	}
	return "EXDATE;TZID=" + e.Timezone + ":" + stamps
}

// setICSExDates writes the skips onto a subscriber-feed VEVENT.
func setICSExDates(ev *ics.VEvent, e *Event) {
	if len(e.ExDates) == 0 {
		return
	}
	stamps := strings.Join(exdateStamps(e), ",")
	if e.AllDay {
		ev.SetProperty(ics.ComponentPropertyExdate, stamps, ics.WithValue("DATE"))
		return
	}
	ev.SetProperty(ics.ComponentPropertyExdate, stamps, ics.WithTZID(e.Loc().String()))
}

// formatSkips lists the skips still ahead, so a caller reading an event back
// can see what update's skip_dates would replace. Empty when there are none.
func formatSkips(e *Event) string {
	now := time.Now()
	loc := e.Loc()
	var parts []string
	for _, d := range e.ExDates {
		if d.Before(now.AddDate(0, 0, -1)) {
			continue
		}
		parts = append(parts, d.In(loc).Format("Mon 2 Jan"))
	}
	if len(parts) == 0 {
		return ""
	}
	return "  skipping: " + strings.Join(parts, ", ") + "\n"
}
