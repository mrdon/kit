package events

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func denverEvent(t *testing.T, start time.Time, extra ...time.Time) *Event {
	t.Helper()
	end := start.Add(2 * time.Hour)
	return &Event{
		Title:    "Supper Club",
		Timezone: "America/Denver",
		StartsAt: start,
		EndsAt:   &end,
		RDates:   extra,
	}
}

// The invariant the whole feature rests on: whatever order dates arrive in,
// starts_at ends up the earliest and rdates holds the rest, sorted.
func TestApplyDatesSortsAndDedupes(t *testing.T) {
	loc := denver(t)
	start := time.Date(2026, 10, 2, 18, 0, 0, 0, loc)
	e := denverEvent(t, start)

	applyDates(e, []time.Time{
		time.Date(2026, 11, 6, 18, 0, 0, 0, loc),
		time.Date(2026, 9, 4, 18, 0, 0, 0, loc),  // earlier than the start
		time.Date(2026, 11, 6, 18, 0, 0, 0, loc), // duplicate
	})

	if got := e.StartsAt.In(loc).Format("2006-01-02"); got != "2026-09-04" {
		t.Fatalf("earliest date should become the start, got %s", got)
	}
	if len(e.RDates) != 2 {
		t.Fatalf("expected 2 extra dates after dedupe, got %d: %v", len(e.RDates), e.RDates)
	}
	if got := e.RDates[0].In(loc).Format("2006-01-02"); got != "2026-10-02" {
		t.Errorf("extra dates out of order: %s", got)
	}
	if got := e.RDates[1].In(loc).Format("2006-01-02"); got != "2026-11-06" {
		t.Errorf("extra dates out of order: %s", got)
	}
}

// EndsAt is an absolute instant, not a duration. When an earlier date takes
// over as the start, the end has to move with it -- otherwise the event silently
// changes length, and since Expand derives every occurrence's duration from the
// pair, the whole series resizes rather than just its first date.
func TestApplyDatesShiftsEndWithTheStart(t *testing.T) {
	loc := denver(t)
	start := time.Date(2026, 10, 2, 18, 0, 0, 0, loc)
	e := denverEvent(t, start)

	applyDates(e, []time.Time{time.Date(2026, 9, 4, 18, 0, 0, 0, loc)})

	if d := e.EndsAt.Sub(e.StartsAt); d != 2*time.Hour {
		t.Fatalf("event length changed to %v, want 2h", d)
	}
	if got := e.EndsAt.In(loc).Format("2006-01-02 15:04"); got != "2026-09-04 20:00" {
		t.Errorf("end did not follow the start: %s", got)
	}
}

func TestApplyDatesWithNoExtrasLeavesAOneOff(t *testing.T) {
	loc := denver(t)
	start := time.Date(2026, 10, 2, 18, 0, 0, 0, loc)
	e := denverEvent(t, start)

	applyDates(e, nil)

	if !e.StartsAt.Equal(start) {
		t.Errorf("start moved: %s", e.StartsAt)
	}
	if len(e.RDates) != 0 {
		t.Errorf("expected no extra dates, got %v", e.RDates)
	}
	if e.Repeats() {
		t.Error("an event with no rule and no extra dates does not repeat")
	}
}

func TestValidateDatesRejectsAllDay(t *testing.T) {
	loc := denver(t)
	start := time.Date(2026, 10, 2, 0, 0, 0, 0, loc)
	e := denverEvent(t, start, time.Date(2026, 11, 6, 0, 0, 0, 0, loc))
	e.AllDay = true

	if err := validateDates(e); err == nil {
		t.Fatal("an all-day event with a date list must be refused; RFC 5545 needs DATE-valued RDATEs")
	}
}

func TestParseDatesSkipsBlankRows(t *testing.T) {
	loc := denver(t)
	// The console's date editor can carry an empty row the user has not filled
	// in yet; that is not an error, it is just not a date.
	got, err := parseDates([]string{"2026-10-02 18:00", "  ", ""}, loc)
	if err != nil {
		t.Fatalf("parseDates: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 date, got %d: %v", len(got), got)
	}
}

func TestAllDatesPutsTheStartFirst(t *testing.T) {
	loc := denver(t)
	start := time.Date(2026, 9, 4, 18, 0, 0, 0, loc)
	e := denverEvent(t, start, time.Date(2026, 10, 2, 18, 0, 0, 0, loc))

	all := e.AllDates()
	if len(all) != 2 {
		t.Fatalf("expected 2 dates, got %d", len(all))
	}
	if !all[0].Equal(start) {
		t.Errorf("first entry should be the start, got %s", all[0])
	}
}

// --- Calendar rendering -----------------------------------------------------

// Without TZID Google reads the values as floating local time in the calendar's
// own default zone, so every date after a DST transition lands an hour out.
func TestRDateLineCarriesTheNamedZone(t *testing.T) {
	loc := denver(t)
	e := denverEvent(t,
		time.Date(2026, 9, 4, 18, 0, 0, 0, loc),
		time.Date(2026, 10, 2, 18, 0, 0, 0, loc),
		time.Date(2026, 11, 6, 18, 0, 0, 0, loc), // after DST ends
	)

	lines := recurrenceLines(e)
	if len(lines) != 1 {
		t.Fatalf("expected one RDATE line, got %v", lines)
	}
	want := "RDATE;TZID=America/Denver:20261002T180000,20261106T180000"
	if lines[0] != want {
		t.Errorf("RDATE line:\n got %s\nwant %s", lines[0], want)
	}
}

func TestRecurrenceLinesComposeRuleAndDates(t *testing.T) {
	loc := denver(t)
	e := denverEvent(t,
		time.Date(2026, 9, 1, 19, 0, 0, 0, loc), // a Tuesday
		time.Date(2026, 12, 24, 19, 0, 0, 0, loc),
	)
	e.RRule = "FREQ=WEEKLY;BYDAY=TU"

	lines := recurrenceLines(e)
	if len(lines) != 2 {
		t.Fatalf("expected an RRULE and an RDATE line, got %v", lines)
	}
	if lines[0] != "RRULE:FREQ=WEEKLY;BYDAY=TU" {
		t.Errorf("first line should be the rule, got %s", lines[0])
	}
	if lines[1] != "RDATE;TZID=America/Denver:20261224T190000" {
		t.Errorf("second line should be the dates, got %s", lines[1])
	}
}

// The sync skips a write when the hash is unchanged, so a date the hash does
// not cover is a date Google never hears about.
func TestContentHashCoversTheDateList(t *testing.T) {
	loc := denver(t)
	start := time.Date(2026, 9, 4, 18, 0, 0, 0, loc)
	base := denverEvent(t, start)
	withDates := denverEvent(t, start, time.Date(2026, 10, 2, 18, 0, 0, 0, loc))
	moved := denverEvent(t, start, time.Date(2026, 10, 9, 18, 0, 0, 0, loc))

	h := func(e *Event) string { return contentHash(buildEvent(e, e.TenantID), "cal") }

	if h(base) == h(withDates) {
		t.Error("adding a date must change the content hash")
	}
	if h(withDates) == h(moved) {
		t.Error("moving a date must change the content hash")
	}
}

// --- Wording ----------------------------------------------------------------

func TestDescribeCadence(t *testing.T) {
	loc := denver(t)
	oct2 := time.Date(2026, 10, 2, 18, 0, 0, 0, loc)
	nov6 := time.Date(2026, 11, 6, 18, 0, 0, 0, loc)

	for _, tc := range []struct {
		name  string
		rule  string
		dates []time.Time
		want  string
	}{
		{name: "one off", want: ""},
		{name: "weekly", rule: "FREQ=WEEKLY;BYDAY=TU", want: "every Tuesday"},
		{name: "first friday", rule: "FREQ=MONTHLY;BYDAY=1FR", want: "every month on the first Friday"},
		{name: "last friday", rule: "FREQ=MONTHLY;BYDAY=-1FR", want: "every month on the last Friday"},
		{name: "day of month", rule: "FREQ=MONTHLY;BYMONTHDAY=15", want: "every month on the 15th"},
		{name: "last day", rule: "FREQ=MONTHLY;BYMONTHDAY=-1", want: "every month on the last day"},
		{name: "quarterly", rule: "FREQ=MONTHLY;INTERVAL=3;BYMONTHDAY=1", want: "every 3 months on the 1st"},
		{name: "date list", dates: []time.Time{oct2, nov6}, want: "on 3 set dates"},
		{
			name: "rule plus a one-off extra",
			rule: "FREQ=WEEKLY;BYDAY=FR", dates: []time.Time{oct2},
			want: "every Friday, plus 1 extra date",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// 2026-09-04 is a Friday and the first Friday of the month, so it
			// satisfies every rule under test.
			e := denverEvent(t, time.Date(2026, 9, 4, 18, 0, 0, 0, loc), tc.dates...)
			e.RRule = tc.rule
			if got := describeCadence(e); got != tc.want {
				t.Errorf("describeCadence:\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestOrdinalSuffix(t *testing.T) {
	for in, want := range map[int]string{
		1: "1st", 2: "2nd", 3: "3rd", 4: "4th",
		11: "11th", 12: "12th", 13: "13th",
		21: "21st", 22: "22nd", 23: "23rd", 31: "31st",
	} {
		if got := ordinalSuffix(in); got != want {
			t.Errorf("ordinalSuffix(%d) = %q, want %q", in, got, want)
		}
	}
}

// --- Link validation --------------------------------------------------------

// The failure this prevents is silent rather than loud. "://" is legal inside a
// path, so a link pasted twice parses as one perfectly valid URL with a very
// long path -- it validates clean, syncs, and publishes to the website as an
// href that is syntactically fine and goes nowhere.
func TestValidateURLRejectsConcatenatedLinks(t *testing.T) {
	one := "https://www.example.com/e/2026-brewfest"
	for _, tc := range []struct {
		name string
		raw  string
		ok   bool
	}{
		{name: "single link", raw: one, ok: true},
		{name: "pasted twice", raw: one + one},
		{name: "pasted many times", raw: strings.Repeat(one, 48)},
		// A URL carrying another in its QUERY is ordinary and must survive.
		{name: "redirect param", raw: "https://tickets.example.com/buy?next=https://www.example.com/thanks", ok: true},
		{name: "runaway length", raw: "https://example.com/" + strings.Repeat("a", maxURLLen)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateURL(tc.raw, "registration link")
			if tc.ok && err != nil {
				t.Fatalf("rejected a valid link: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("accepted a malformed link of %d chars", len(tc.raw))
			}
		})
	}
}

// --- What the list surfaces -------------------------------------------------

// The reason next_occurrence exists: starts_at is the FIRST occurrence, so for
// any established series it is months behind, and an events list read to find
// out "what's coming up" would show a date that has already been and gone.
func TestNextOccurrenceSkipsPastDates(t *testing.T) {
	loc := denver(t)
	now := time.Now().In(loc)

	past := now.AddDate(0, -5, 0)
	start := time.Date(past.Year(), past.Month(), past.Day(), 13, 0, 0, 0, loc)
	e := denverEvent(t, start)
	e.RRule = "FREQ=MONTHLY"

	next := e.NextOccurrence()
	if next == nil {
		t.Fatal("an ongoing monthly series has no next occurrence")
	}
	if next.Before(now.AddDate(0, 0, -1)) {
		t.Errorf("next occurrence %s is in the past", next)
	}
	if next.In(loc).Day() != start.Day() {
		t.Errorf("next occurrence %s is not on the series' day of the month (%d)", next, start.Day())
	}
}

// The same for a hand-picked list: the first two dates gone, the third ahead.
func TestNextOccurrencePicksTheFirstFutureDate(t *testing.T) {
	loc := denver(t)
	now := time.Now().In(loc)
	at := func(days int) time.Time {
		d := now.AddDate(0, 0, days)
		return time.Date(d.Year(), d.Month(), d.Day(), 13, 0, 0, 0, loc)
	}
	e := denverEvent(t, at(-60), at(-30), at(30), at(60))

	next := e.NextOccurrence()
	if next == nil {
		t.Fatal("a series with dates still ahead has no next occurrence")
	}
	if got, want := next.In(loc).Format("2006-01-02"), at(30).Format("2006-01-02"); got != want {
		t.Errorf("next = %s, want %s (the first date still ahead)", got, want)
	}
}

// An event today is still today's event at 4pm. Dropping it the moment it
// starts is wrong in exactly the hour people are most likely to be looking.
func TestNextOccurrenceKeepsAnEventRunningToday(t *testing.T) {
	loc := denver(t)
	now := time.Now().In(loc)
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 30, 0, 0, loc)
	e := denverEvent(t, start, start.AddDate(0, 0, 30))

	next := e.NextOccurrence()
	if next == nil {
		t.Fatal("today's event was dropped")
	}
	if next.In(loc).Format("2006-01-02") != now.Format("2006-01-02") {
		t.Errorf("next = %s, want today", next)
	}
}

func TestNextOccurrenceIsNilOnceEveryDateHasPassed(t *testing.T) {
	loc := denver(t)
	now := time.Now().In(loc)
	start := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, loc).AddDate(0, 0, -60)
	e := denverEvent(t, start, start.AddDate(0, 0, 10))

	if next := e.NextOccurrence(); next != nil {
		t.Errorf("a finished series reported a next occurrence: %s", next)
	}
}

// The console reads these off the JSON, so they have to actually be on it.
func TestEventJSONCarriesTheNextOccurrence(t *testing.T) {
	loc := denver(t)
	now := time.Now().In(loc)
	at := func(days int) time.Time {
		d := now.AddDate(0, 0, days)
		return time.Date(d.Year(), d.Month(), d.Day(), 13, 0, 0, 0, loc)
	}

	series := denverEvent(t, at(-30), at(30), at(60))
	blob, err := json.Marshal(series)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(blob, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	next, ok := got["next_occurrence"].(string)
	if !ok {
		t.Fatalf("next_occurrence missing from %s", blob)
	}
	if !strings.HasPrefix(next, at(30).Format("2006-01-02")) {
		t.Errorf("next_occurrence = %s, want %s", next, at(30).Format("2006-01-02"))
	}
	if n, _ := got["date_count"].(float64); int(n) != 3 {
		t.Errorf("date_count = %v, want 3 (two extras plus the start)", got["date_count"])
	}

	// A one-off carries no date_count: starts_at already answers it, and an
	// omitted field is easier for the client than a redundant one.
	oneOff := denverEvent(t, at(30))
	blob, err = json.Marshal(oneOff)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(blob), `"date_count"`) {
		t.Errorf("a one-off should carry no date_count: %s", blob)
	}
	if !strings.Contains(string(blob), `"next_occurrence"`) {
		t.Errorf("an upcoming one-off should still report its date: %s", blob)
	}
}
