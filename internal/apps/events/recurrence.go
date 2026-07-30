package events

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Recurrence support is deliberately a strict allowlist: FREQ=WEEKLY only,
// with optional INTERVAL, BYDAY, and one of UNTIL/COUNT.
//
// The reason is a failure mode that is silent rather than loud. Kit renders
// the stored rule straight to Google, which understands the whole RFC 5545
// grammar; our own expander understands this subset. Store a rule we can
// render but not expand -- FREQ=MONTHLY;BYSETPOS=-1, say -- and Google draws a
// perfect series on the calendar while every Kit date query sees nothing at
// all. That is a wrong answer, not a crash. So anything Expand cannot read is
// refused on write.
//
// Weekly covers the one genuinely recurring event here (trivia, identical
// every week). Live music is not recurring: each night is a distinct event
// with its own performer, authored per night.

// maxOccurrences bounds a single Expand call. A rule with neither UNTIL nor
// COUNT is unbounded by definition, so the window is what stops it -- this cap
// is a backstop against a caller passing an absurd window, not the mechanism.
const maxOccurrences = 5000

var weekdayCodes = map[string]time.Weekday{
	"SU": time.Sunday,
	"MO": time.Monday,
	"TU": time.Tuesday,
	"WE": time.Wednesday,
	"TH": time.Thursday,
	"FR": time.Friday,
	"SA": time.Saturday,
}

var weekdayNames = map[time.Weekday]string{
	time.Sunday:    "SU",
	time.Monday:    "MO",
	time.Tuesday:   "TU",
	time.Wednesday: "WE",
	time.Thursday:  "TH",
	time.Friday:    "FR",
	time.Saturday:  "SA",
}

// Rule is a parsed weekly recurrence. The zero value is not meaningful; use
// ParseRule.
type Rule struct {
	Interval int            // weeks between repeats, >= 1
	Days     []time.Weekday // sorted; empty means "the start's own weekday"
	Until    time.Time      // zero means unbounded
	Count    int            // 0 means unbounded
}

// ErrUnsupportedRule is the sentinel for every rejection, so callers can map
// the whole class to one user-facing message.
var ErrUnsupportedRule = errors.New("unsupported recurrence rule")

// ParseRule parses the supported RRULE subset. The input may carry the
// "RRULE:" prefix or not.
//
// An empty input yields (nil, nil): "no recurrence" is a valid, expected state
// rather than an error, and every caller already treats a nil rule as
// non-recurring (see Expand). A sentinel error here would make the common case
// -- a one-off event -- the error path.
//
//nolint:nilnil // (nil, nil) means "not recurring", which is not a failure
func ParseRule(s string) (*Rule, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	s = strings.TrimPrefix(strings.ToUpper(s), "RRULE:")

	r := &Rule{Interval: 1}
	seenFreq := false
	for part := range strings.SplitSeq(s, ";") {
		if part == "" {
			continue
		}
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("%w: malformed segment %q", ErrUnsupportedRule, part)
		}
		var err error
		switch key {
		case "FREQ":
			if value != "WEEKLY" {
				return nil, fmt.Errorf("%w: only FREQ=WEEKLY is supported, got %q", ErrUnsupportedRule, value)
			}
			seenFreq = true
		case "INTERVAL":
			if r.Interval, err = strconv.Atoi(value); err != nil || r.Interval < 1 {
				return nil, fmt.Errorf("%w: INTERVAL must be a positive integer, got %q", ErrUnsupportedRule, value)
			}
		case "BYDAY":
			if r.Days, err = parseDays(value); err != nil {
				return nil, err
			}
		case "UNTIL":
			if r.Until, err = parseUntil(value); err != nil {
				return nil, err
			}
		case "COUNT":
			if r.Count, err = strconv.Atoi(value); err != nil || r.Count < 1 {
				return nil, fmt.Errorf("%w: COUNT must be a positive integer, got %q", ErrUnsupportedRule, value)
			}
		default:
			return nil, fmt.Errorf("%w: %s is not supported", ErrUnsupportedRule, key)
		}
	}
	if !seenFreq {
		return nil, fmt.Errorf("%w: FREQ is required", ErrUnsupportedRule)
	}
	if r.Count > 0 && !r.Until.IsZero() {
		return nil, fmt.Errorf("%w: UNTIL and COUNT are mutually exclusive", ErrUnsupportedRule)
	}
	return r, nil
}

func parseDays(value string) ([]time.Weekday, error) {
	var days []time.Weekday
	seen := map[time.Weekday]bool{}
	for code := range strings.SplitSeq(value, ",") {
		// A numeric prefix (e.g. "-1FR", "2TU") selects the nth weekday of the
		// period. It is meaningless for FREQ=WEEKLY and we do not expand it.
		wd, ok := weekdayCodes[strings.TrimSpace(code)]
		if !ok {
			return nil, fmt.Errorf("%w: BYDAY value %q", ErrUnsupportedRule, code)
		}
		if !seen[wd] {
			seen[wd] = true
			days = append(days, wd)
		}
	}
	if len(days) == 0 {
		return nil, fmt.Errorf("%w: BYDAY is empty", ErrUnsupportedRule)
	}
	slices.Sort(days)
	return days, nil
}

// parseUntil accepts the two RFC 5545 forms: a UTC date-time (20261231T065959Z)
// and a bare date (20261231). A floating date-time without the Z is rejected --
// its meaning depends on a timezone the rule doesn't carry.
func parseUntil(value string) (time.Time, error) {
	for _, layout := range []string{"20060102T150405Z", "20060102"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("%w: UNTIL must be YYYYMMDD or YYYYMMDDTHHMMSSZ, got %q", ErrUnsupportedRule, value)
}

// String renders the canonical form, without the "RRULE:" prefix. Field order
// is fixed so the value is stable for storage and content hashing.
func (r *Rule) String() string {
	if r == nil {
		return ""
	}
	parts := []string{"FREQ=WEEKLY"}
	if r.Interval > 1 {
		parts = append(parts, "INTERVAL="+strconv.Itoa(r.Interval))
	}
	if len(r.Days) > 0 {
		codes := make([]string, len(r.Days))
		for i, d := range r.Days {
			codes[i] = weekdayNames[d]
		}
		parts = append(parts, "BYDAY="+strings.Join(codes, ","))
	}
	if !r.Until.IsZero() {
		parts = append(parts, "UNTIL="+r.Until.UTC().Format("20060102T150405Z"))
	}
	if r.Count > 0 {
		parts = append(parts, "COUNT="+strconv.Itoa(r.Count))
	}
	return strings.Join(parts, ";")
}

// CoversWeekday reports whether the rule fires on the given weekday. An empty
// BYDAY inherits the start's weekday, so the caller passes that in.
func (r *Rule) CoversWeekday(startWeekday time.Weekday) bool {
	if r == nil {
		return false
	}
	if len(r.Days) == 0 {
		return true
	}
	return slices.Contains(r.Days, startWeekday)
}

// Occurrence is one instance of an event, as wall-clock instants.
type Occurrence struct {
	Start time.Time
	End   time.Time
}

// Expand returns the occurrences that start within [from, to).
//
// rule may be nil, which yields the single occurrence at start -- so callers
// need no special case for non-recurring events.
//
// DST correctness is the whole point of this function. Repeats advance by
// CALENDAR DAYS in the event's own named zone, never by adding 7*24h: 7pm
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
