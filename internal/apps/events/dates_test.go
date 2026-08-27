package events

import (
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
