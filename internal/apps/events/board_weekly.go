package events

import (
	"slices"
	"strings"
	"time"
)

// boardWeekly is the weekly agenda: every series that repeats on a rule and
// is on in the coming week, Sunday first so it reads as a week.
//
// Built from the series rather than typed, so a renamed night is renamed on
// the wall too, and a series that has ended drops off by itself. When more
// than boardWeeklyMax qualify, featured beats normal before the cut is made.
// Series on a list of set dates are not weekly, whatever their rhythm looks
// like: those are separate nights, and boardUpcoming gives them a poster.
func boardWeekly(events []Event, now time.Time) []BoardWeekly {
	end := dayStart(now).AddDate(0, 0, boardHorizonDays)
	type line struct {
		w        BoardWeekly
		featured bool
		first    time.Weekday
		clock    int
	}
	var lines []line
	for i := range events {
		e := &events[i]
		if !onTheWall(e) || e.RRule == "" {
			continue
		}
		var days []time.Weekday
		var start, stop time.Time
		for _, occ := range e.Occurrences(dayStart(now), end) {
			at := occ.Start.In(now.Location())
			if !slices.Contains(days, at.Weekday()) {
				days = append(days, at.Weekday())
			}
			if start.IsZero() {
				start, stop = at, occ.End.In(now.Location())
			}
		}
		if len(days) == 0 {
			continue
		}
		slices.Sort(days)
		w := BoardWeekly{When: everyDays(days), Title: strings.TrimSpace(e.Title), Note: weeklyNote(e)}
		if !e.AllDay {
			w.Time = timeRange(start, stop)
		}
		lines = append(lines, line{w, e.IsFeatured(), days[0], start.Hour()*60 + start.Minute()})
	}
	slices.SortStableFunc(lines, func(a, b line) int {
		switch {
		case a.featured == b.featured:
			return 0
		case a.featured:
			return -1
		}
		return 1
	})
	lines = lines[:min(len(lines), boardWeeklyMax)]
	slices.SortStableFunc(lines, func(a, b line) int {
		if a.first != b.first {
			return int(a.first) - int(b.first)
		}
		return a.clock - b.clock
	})
	out := make([]BoardWeekly, len(lines))
	for i, l := range lines {
		out[i] = l.w
	}
	return out
}

// everyDays says which days a series is on: "Every Mon", "Every Mon & Tue",
// "Every Mon, Wed & Fri".
func everyDays(days []time.Weekday) string {
	names := make([]string, len(days))
	for i, d := range days {
		names[i] = d.String()[:3]
	}
	if len(names) == 1 {
		return "Every " + names[0]
	}
	return "Every " + strings.Join(names[:len(names)-1], ", ") + " & " + names[len(names)-1]
}

// timeRange is "5 to 9pm", "11am to 2pm", or "6:30pm" when the end is not
// worth saying. House style: "to", never a dash.
func timeRange(start, end time.Time) string {
	from := topperTime(start)
	if !end.After(start) {
		return from
	}
	to := topperTime(end)
	if start.Format("pm") == end.Format("pm") {
		from = strings.TrimSuffix(strings.TrimSuffix(from, "am"), "pm")
	}
	return from + " to " + to
}

// weeklyNote is the summary's first sentence, kept to one line on the wall.
func weeklyNote(e *Event) string {
	s := strings.TrimSpace(e.Summary)
	if i := strings.Index(s, ". "); i >= 0 {
		s = s[:i+1]
	}
	s = strings.TrimSuffix(s, ".")
	return truncateWords(s, boardNoteMax)
}
