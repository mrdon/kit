package events

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Recurrence support is deliberately a strict allowlist: FREQ=WEEKLY and
// FREQ=MONTHLY, with optional INTERVAL, BYDAY/BYMONTHDAY, and one of
// UNTIL/COUNT.
//
// The reason is a failure mode that is silent rather than loud. Kit renders
// the stored rule straight to Google, which understands the whole RFC 5545
// grammar; our own expander understands this subset. Store a rule we can
// render but not expand -- FREQ=YEARLY;BYWEEKNO=13, say -- and Google draws a
// perfect series on the calendar while every Kit date query sees nothing at
// all. That is a wrong answer, not a crash. So anything Expand cannot read is
// refused on write.
//
// Weekly covers trivia. Monthly covers the "first Friday" and "the 15th"
// shapes, which are the cadences a venue actually schedules on and which
// previously had to be authored one event per month. Anything irregular --
// dates picked around a chef's availability, a series with a gap over a
// holiday -- is an explicit date list instead; see Series.

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

// Freq is the supported subset of RFC 5545 FREQ values.
type Freq string

const (
	FreqWeekly  Freq = "WEEKLY"
	FreqMonthly Freq = "MONTHLY"
)

// OrdDay is one BYDAY entry. Ord is the ordinal prefix: 0 means "every such
// weekday in the month", 1..5 selects the nth, and -1..-5 counts back from the
// end, so "-1FR" is the last Friday. Ord is always 0 under FREQ=WEEKLY, where
// an ordinal has no meaning.
type OrdDay struct {
	Ord int
	Day time.Weekday
}

// Rule is a parsed recurrence. The zero value is not meaningful; use ParseRule.
type Rule struct {
	Freq     Freq
	Interval int // periods between repeats, >= 1

	// Days is the weekly day set: sorted, and empty means "the start's own
	// weekday". Kept as a plain weekday slice because FREQ=WEEKLY can never
	// carry an ordinal, and every existing caller reads it this way.
	Days []time.Weekday

	// MonthDays and OrdDays are the monthly day selectors, mutually exclusive
	// and both optional. With neither, the series falls on the start's own day
	// of the month.
	MonthDays []int    // BYMONTHDAY: 1..31, or -1..-31 counting from the end
	OrdDays   []OrdDay // BYDAY with optional ordinal prefix

	Until time.Time // zero means unbounded
	Count int       // 0 means unbounded
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
// Segments are collected before being interpreted because BYDAY's grammar
// depends on FREQ ("1FR" is legal monthly, meaningless weekly) and RFC 5545
// does not require FREQ to come first.
//
//nolint:nilnil // (nil, nil) means "not recurring", which is not a failure
func ParseRule(s string) (*Rule, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	s = strings.TrimPrefix(strings.ToUpper(s), "RRULE:")

	parts, err := splitRule(s)
	if err != nil {
		return nil, err
	}
	r := &Rule{Interval: 1}
	switch parts["FREQ"] {
	case "WEEKLY":
		r.Freq = FreqWeekly
	case "MONTHLY":
		r.Freq = FreqMonthly
	case "":
		return nil, fmt.Errorf("%w: FREQ is required", ErrUnsupportedRule)
	default:
		return nil, fmt.Errorf("%w: only FREQ=WEEKLY and FREQ=MONTHLY are supported, got %q",
			ErrUnsupportedRule, parts["FREQ"])
	}
	if v, ok := parts["INTERVAL"]; ok {
		if r.Interval, err = strconv.Atoi(v); err != nil || r.Interval < 1 {
			return nil, fmt.Errorf("%w: INTERVAL must be a positive integer, got %q", ErrUnsupportedRule, v)
		}
	}
	if v, ok := parts["UNTIL"]; ok {
		if r.Until, err = parseUntil(v); err != nil {
			return nil, err
		}
	}
	if v, ok := parts["COUNT"]; ok {
		if r.Count, err = strconv.Atoi(v); err != nil || r.Count < 1 {
			return nil, fmt.Errorf("%w: COUNT must be a positive integer, got %q", ErrUnsupportedRule, v)
		}
	}
	if r.Count > 0 && !r.Until.IsZero() {
		return nil, fmt.Errorf("%w: UNTIL and COUNT are mutually exclusive", ErrUnsupportedRule)
	}
	if err := parseDaySelectors(r, parts); err != nil {
		return nil, err
	}
	return r, nil
}

// splitRule breaks the rule into its KEY=VALUE segments, rejecting unknown
// keys and repeats. A duplicate is refused rather than last-wins: two
// conflicting BYDAYs mean the author expected something we would not deliver.
func splitRule(s string) (map[string]string, error) {
	known := map[string]bool{
		"FREQ": true, "INTERVAL": true, "BYDAY": true,
		"BYMONTHDAY": true, "UNTIL": true, "COUNT": true,
	}
	out := map[string]string{}
	for part := range strings.SplitSeq(s, ";") {
		if part == "" {
			continue
		}
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("%w: malformed segment %q", ErrUnsupportedRule, part)
		}
		if !known[key] {
			return nil, fmt.Errorf("%w: %s is not supported", ErrUnsupportedRule, key)
		}
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("%w: %s appears twice", ErrUnsupportedRule, key)
		}
		out[key] = value
	}
	return out, nil
}

// parseDaySelectors reads BYDAY and BYMONTHDAY under the rule's own frequency.
func parseDaySelectors(r *Rule, parts map[string]string) error {
	byDay, hasDay := parts["BYDAY"]
	byMonthDay, hasMonthDay := parts["BYMONTHDAY"]

	if r.Freq == FreqWeekly {
		if hasMonthDay {
			return fmt.Errorf("%w: BYMONTHDAY needs FREQ=MONTHLY", ErrUnsupportedRule)
		}
		if !hasDay {
			return nil
		}
		days, err := parseWeekdays(byDay)
		if err != nil {
			return err
		}
		r.Days = days
		return nil
	}

	// Monthly. BYDAY and BYMONTHDAY together is legal RFC 5545 -- it
	// intersects the two sets -- but the intersection is rarely what an author
	// means and we do not expand it, so it is refused rather than mis-expanded.
	if hasDay && hasMonthDay {
		return fmt.Errorf("%w: use either BYDAY or BYMONTHDAY, not both", ErrUnsupportedRule)
	}
	switch {
	case hasMonthDay:
		days, err := parseMonthDays(byMonthDay)
		if err != nil {
			return err
		}
		r.MonthDays = days
	case hasDay:
		days, err := parseOrdDays(byDay)
		if err != nil {
			return err
		}
		r.OrdDays = days
	}
	return nil
}

// parseWeekdays reads a plain BYDAY list for FREQ=WEEKLY.
func parseWeekdays(value string) ([]time.Weekday, error) {
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

// parseOrdDays reads a BYDAY list for FREQ=MONTHLY, where each entry may carry
// an ordinal prefix: "FR" (every Friday), "1FR" (first), "-1FR" (last).
func parseOrdDays(value string) ([]OrdDay, error) {
	var out []OrdDay
	seen := map[OrdDay]bool{}
	for code := range strings.SplitSeq(value, ",") {
		code = strings.TrimSpace(code)
		if len(code) < 2 {
			return nil, fmt.Errorf("%w: BYDAY value %q", ErrUnsupportedRule, code)
		}
		prefix, suffix := code[:len(code)-2], code[len(code)-2:]
		wd, ok := weekdayCodes[suffix]
		if !ok {
			return nil, fmt.Errorf("%w: BYDAY value %q", ErrUnsupportedRule, code)
		}
		ord := 0
		if prefix != "" {
			n, err := strconv.Atoi(prefix)
			// The 5th of a weekday exists in some months and not others; beyond
			// that the ordinal is always empty, so it is a typo, not a rule.
			if err != nil || n == 0 || n < -5 || n > 5 {
				return nil, fmt.Errorf("%w: BYDAY ordinal in %q must be 1..5 or -1..-5", ErrUnsupportedRule, code)
			}
			ord = n
		}
		od := OrdDay{Ord: ord, Day: wd}
		if !seen[od] {
			seen[od] = true
			out = append(out, od)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: BYDAY is empty", ErrUnsupportedRule)
	}
	slices.SortFunc(out, func(a, b OrdDay) int {
		if a.Ord != b.Ord {
			return a.Ord - b.Ord
		}
		return int(a.Day) - int(b.Day)
	})
	return out, nil
}

// parseMonthDays reads BYMONTHDAY. Negative values count back from the end of
// the month, so -1 is the last day whatever its number.
func parseMonthDays(value string) ([]int, error) {
	var out []int
	seen := map[int]bool{}
	for field := range strings.SplitSeq(value, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(field))
		if err != nil || n == 0 || n < -31 || n > 31 {
			return nil, fmt.Errorf("%w: BYMONTHDAY value %q must be 1..31 or -1..-31", ErrUnsupportedRule, field)
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: BYMONTHDAY is empty", ErrUnsupportedRule)
	}
	slices.Sort(out)
	return out, nil
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
	freq := r.Freq
	if freq == "" {
		freq = FreqWeekly
	}
	parts := []string{"FREQ=" + string(freq)}
	if r.Interval > 1 {
		parts = append(parts, "INTERVAL="+strconv.Itoa(r.Interval))
	}
	switch {
	case len(r.Days) > 0:
		codes := make([]string, len(r.Days))
		for i, d := range r.Days {
			codes[i] = weekdayNames[d]
		}
		parts = append(parts, "BYDAY="+strings.Join(codes, ","))
	case len(r.OrdDays) > 0:
		codes := make([]string, len(r.OrdDays))
		for i, od := range r.OrdDays {
			if od.Ord == 0 {
				codes[i] = weekdayNames[od.Day]
			} else {
				codes[i] = strconv.Itoa(od.Ord) + weekdayNames[od.Day]
			}
		}
		parts = append(parts, "BYDAY="+strings.Join(codes, ","))
	case len(r.MonthDays) > 0:
		codes := make([]string, len(r.MonthDays))
		for i, d := range r.MonthDays {
			codes[i] = strconv.Itoa(d)
		}
		parts = append(parts, "BYMONTHDAY="+strings.Join(codes, ","))
	}
	if !r.Until.IsZero() {
		parts = append(parts, "UNTIL="+r.Until.UTC().Format("20060102T150405Z"))
	}
	if r.Count > 0 {
		parts = append(parts, "COUNT="+strconv.Itoa(r.Count))
	}
	return strings.Join(parts, ";")
}

// CoversWeekday reports whether a weekly rule fires on the given weekday. An
// empty BYDAY inherits the start's weekday, so the caller passes that in.
func (r *Rule) CoversWeekday(startWeekday time.Weekday) bool {
	if r == nil {
		return false
	}
	if len(r.Days) == 0 {
		return true
	}
	return slices.Contains(r.Days, startWeekday)
}

// Covers reports whether the rule fires on the given local start date.
//
// This is the DTSTART agreement check. RFC 5545 treats DTSTART as an
// occurrence even when it does not match the rule's own selectors, so a
// mismatch leaves Google showing a stray first instance that Kit's expander
// never emits. Refusing the mismatch on write keeps the two views identical.
func (r *Rule) Covers(start time.Time) bool {
	if r == nil {
		return false
	}
	if r.Freq == FreqMonthly {
		return slices.Contains(monthDays(start.Year(), start.Month(), r, start.Day()), start.Day())
	}
	return r.CoversWeekday(start.Weekday())
}
