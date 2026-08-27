package events

import (
	"slices"
	"time"
)

// Expansion: turning a stored rule and date list into concrete occurrences.
// The parsing and validation half lives in recurrence.go.

// Occurrence is one instance of an event, as wall-clock instants.
type Occurrence struct {
	Start time.Time
	End   time.Time
}

// Series is everything needed to place an event on a calendar: an anchor
// occurrence, an optional rule, and an optional list of explicit extra dates.
//
// The two repeat mechanisms compose rather than compete, exactly as RFC 5545
// composes RRULE and RDATE. In practice an event uses one or the other, but a
// rule plus a one-off extra date is a real case (a monthly market that also
// runs on New Year's Eve) and costs nothing to allow.
type Series struct {
	Start  time.Time
	End    time.Time
	Loc    *time.Location
	Rule   *Rule
	RDates []time.Time
}

// Expand returns every occurrence starting within [from, to), from both the
// rule and the explicit dates, deduped and in chronological order.
//
// UNTIL and COUNT bound the RULE only, never the explicit dates. That is RFC
// 5545's own reading, and it is the useful one: an explicit date is something
// a person typed, so a rule's end condition should not silently swallow it.
func (s Series) Expand(from, to time.Time) []Occurrence {
	loc := s.Loc
	if loc == nil {
		loc = time.UTC
	}
	out := Expand(s.Start, s.End, loc, s.Rule, from, to)

	if len(s.RDates) > 0 {
		duration := max(s.End.Sub(s.Start), 0)
		for _, rd := range s.RDates {
			local := rd.In(loc)
			if local.Before(from) || !local.Before(to) {
				continue
			}
			out = append(out, Occurrence{Start: local, End: local.Add(duration)})
		}
	}

	slices.SortFunc(out, func(a, b Occurrence) int { return a.Start.Compare(b.Start) })
	// Dedupe by instant: a date list that happens to name a day the rule
	// already covers should yield one occurrence, not two stacked entries.
	return slices.CompactFunc(out, func(a, b Occurrence) bool { return a.Start.Equal(b.Start) })
}

// Expand returns the rule's occurrences that start within [from, to).
//
// rule may be nil, which yields the single occurrence at start -- so callers
// need no special case for non-recurring events.
//
// DST correctness is the whole point of this function. Repeats advance by
// CALENDAR units in the event's own named zone, never by adding 7*24h: 7pm
// trivia stays at 7pm across a spring-forward or fall-back boundary, where
// duration arithmetic would silently shift it to 6pm or 8pm. time.Date
// normalises within the zone, which is what makes that work.
//
// The event's duration is preserved rather than its end wall-clock; a
// three-hour event stays three hours.
func Expand(start, end time.Time, loc *time.Location, rule *Rule, from, to time.Time) []Occurrence {
	if loc == nil {
		loc = time.UTC
	}
	duration := max(end.Sub(start), 0)

	local := start.In(loc)
	if rule == nil {
		if !local.Before(from) && local.Before(to) {
			return []Occurrence{{Start: local, End: local.Add(duration)}}
		}
		return nil
	}
	if rule.Freq == FreqMonthly {
		return expandMonthly(local, duration, loc, rule, from, to)
	}
	return expandWeekly(local, duration, loc, rule, from, to)
}

func expandWeekly(local time.Time, duration time.Duration, loc *time.Location, rule *Rule, from, to time.Time) []Occurrence {
	days := rule.Days
	if len(days) == 0 {
		days = []time.Weekday{local.Weekday()}
	}
	hour, minute, sec := local.Clock()

	// Walk from the start of the week containing DTSTART so a multi-day BYDAY
	// emits earlier weekdays in that first week too.
	weekStart := local.AddDate(0, 0, -int(local.Weekday()))
	var out []Occurrence
	emitted := 0

	for week := 0; ; week++ {
		base := weekStart.AddDate(0, 0, week*7*rule.Interval)
		if base.After(to) && week > 0 {
			break
		}
		for _, wd := range days {
			d := base.AddDate(0, 0, int(wd)-int(base.Weekday()))
			// Rebuild from calendar fields in loc -- this is the DST-safe step.
			occ := time.Date(d.Year(), d.Month(), d.Day(), hour, minute, sec, 0, loc)
			if occ.Before(local) {
				continue // before DTSTART
			}
			if !rule.Until.IsZero() && occ.After(rule.Until) {
				return out
			}
			emitted++
			if rule.Count > 0 && emitted > rule.Count {
				return out
			}
			if !occ.Before(to) {
				return out
			}
			if !occ.Before(from) {
				out = append(out, Occurrence{Start: occ, End: occ.Add(duration)})
				if len(out) >= maxOccurrences {
					return out
				}
			}
		}
	}
	return out
}

// expandMonthly walks whole months, resolving each month's day set fresh.
//
// Resolving per month is what makes "the last Friday" and "the 31st" behave:
// both land on a different date each month, and the 31st simply does not exist
// in some. RFC 5545 skips a month whose selector names no valid date rather
// than rolling into the next one -- which is also why the day number is bounds
// checked before time.Date sees it, since time.Date would happily normalise
// February 31st into March 3rd.
func expandMonthly(local time.Time, duration time.Duration, loc *time.Location, rule *Rule, from, to time.Time) []Occurrence {
	hour, minute, sec := local.Clock()
	anchorDay := local.Day()
	// Anchor on the first of DTSTART's month so INTERVAL steps land cleanly;
	// stepping from the 31st would skip short months entirely.
	monthStart := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, loc)

	var out []Occurrence
	emitted := 0

	for month := 0; ; month++ {
		base := monthStart.AddDate(0, month*rule.Interval, 0)
		if base.After(to) && month > 0 {
			break
		}
		for _, day := range monthDays(base.Year(), base.Month(), rule, anchorDay) {
			occ := time.Date(base.Year(), base.Month(), day, hour, minute, sec, 0, loc)
			if occ.Before(local) {
				continue // before DTSTART
			}
			if !rule.Until.IsZero() && occ.After(rule.Until) {
				return out
			}
			emitted++
			if rule.Count > 0 && emitted > rule.Count {
				return out
			}
			if !occ.Before(to) {
				return out
			}
			if !occ.Before(from) {
				out = append(out, Occurrence{Start: occ, End: occ.Add(duration)})
				if len(out) >= maxOccurrences {
					return out
				}
			}
		}
	}
	return out
}

// monthDays resolves a monthly rule's selectors against one concrete month,
// returning the days of that month it fires on, ascending. anchorDay is
// DTSTART's own day, used when the rule names no selector at all.
func monthDays(year int, month time.Month, rule *Rule, anchorDay int) []int {
	last := daysInMonth(year, month)
	var out []int
	add := func(d int) {
		if d >= 1 && d <= last && !slices.Contains(out, d) {
			out = append(out, d)
		}
	}

	switch {
	case len(rule.MonthDays) > 0:
		for _, d := range rule.MonthDays {
			if d < 0 {
				d = last + 1 + d // -1 is the last day of the month
			}
			add(d)
		}
	case len(rule.OrdDays) > 0:
		for _, od := range rule.OrdDays {
			all := weekdayDates(year, month, od.Day, last)
			switch {
			case od.Ord == 0:
				// No ordinal: every such weekday in the month.
				for _, d := range all {
					add(d)
				}
			case od.Ord > 0:
				// "5FR" names a fifth Friday, which most months do not have.
				// RFC 5545 skips the month rather than clamping to the fourth.
				if idx := od.Ord - 1; idx < len(all) {
					add(all[idx])
				}
			default:
				if idx := len(all) + od.Ord; idx >= 0 {
					add(all[idx])
				}
			}
		}
	default:
		add(anchorDay)
	}
	slices.Sort(out)
	return out
}

// weekdayDates lists the days of the month falling on a given weekday.
func weekdayDates(year int, month time.Month, wd time.Weekday, last int) []int {
	// UTC is safe here: only the calendar fields are read, and the first of a
	// month never falls in a DST gap.
	first := time.Date(year, month, 1, 12, 0, 0, 0, time.UTC)
	offset := (int(wd) - int(first.Weekday()) + 7) % 7
	var out []int
	for d := 1 + offset; d <= last; d += 7 {
		out = append(out, d)
	}
	return out
}

func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 12, 0, 0, 0, time.UTC).Day()
}
