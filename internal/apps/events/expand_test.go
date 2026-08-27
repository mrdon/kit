package events

import (
	"testing"
	"time"
)

// Expansion behaviour: turning a rule and a date list into occurrences.
// Parsing and validation of the rule text itself lives in recurrence_test.go.

func TestExpandKeepsWallClockAcrossSpringForward(t *testing.T) {
	loc := denver(t)
	// DST begins 2027-03-14 in the US. Start two Tuesdays before it.
	start := time.Date(2027, 3, 2, 19, 0, 0, 0, loc)
	got := Expand(start, start.Add(3*time.Hour), loc, mustRule(t, "FREQ=WEEKLY;BYDAY=TU"),
		start, time.Date(2027, 4, 1, 0, 0, 0, 0, loc))

	// March 2027 has five Tuesdays: 2, 9, 16, 23, 30.
	if len(got) != 5 {
		t.Fatalf("expected 5 Tuesdays in March, got %d", len(got))
	}
	for _, occ := range got {
		h, m, _ := occ.Start.Clock()
		if h != 19 || m != 0 {
			t.Errorf("%s: clock = %02d:%02d, want 19:00 — DST shifted the event",
				occ.Start.Format("2006-01-02 MST"), h, m)
		}
		if occ.Start.Weekday() != time.Tuesday {
			t.Errorf("%s is not a Tuesday", occ.Start.Format("2006-01-02"))
		}
	}
	assertCrossesDST(t, got)
}

// assertCrossesDST fails if every occurrence sits at the same UTC offset --
// without this, a wall-clock assertion could pass on a window that never
// straddles a transition and prove nothing.
func assertCrossesDST(t *testing.T, got []Occurrence) {
	t.Helper()
	if len(got) < 2 {
		t.Fatalf("need at least 2 occurrences to straddle a transition, got %d", len(got))
	}
	_, first := got[0].Start.Zone()
	_, last := got[len(got)-1].Start.Zone()
	if first == last {
		t.Fatal("window did not cross a DST boundary — test is not exercising anything")
	}
}

func TestExpandKeepsWallClockAcrossFallBack(t *testing.T) {
	loc := denver(t)
	// DST ends 2026-11-01 in the US.
	start := time.Date(2026, 10, 20, 19, 0, 0, 0, loc)
	got := Expand(start, start.Add(3*time.Hour), loc, mustRule(t, "FREQ=WEEKLY;BYDAY=TU"),
		start, time.Date(2026, 11, 30, 0, 0, 0, 0, loc))

	if len(got) < 4 {
		t.Fatalf("expected at least 4 occurrences, got %d", len(got))
	}
	for _, occ := range got {
		if h, m, _ := occ.Start.Clock(); h != 19 || m != 0 {
			t.Errorf("%s: clock = %02d:%02d, want 19:00", occ.Start.Format("2006-01-02 MST"), h, m)
		}
	}
	assertCrossesDST(t, got)
}

func TestExpandPreservesDuration(t *testing.T) {
	loc := denver(t)
	start := time.Date(2026, 8, 4, 19, 0, 0, 0, loc)
	got := Expand(start, start.Add(150*time.Minute), loc, mustRule(t, "FREQ=WEEKLY;BYDAY=TU"),
		start, start.AddDate(0, 0, 21))
	if len(got) == 0 {
		t.Fatal("no occurrences")
	}
	for _, occ := range got {
		if d := occ.End.Sub(occ.Start); d != 150*time.Minute {
			t.Errorf("%s: duration = %v, want 2h30m", occ.Start.Format("2006-01-02"), d)
		}
	}
}

// A nil rule yields exactly one occurrence, so every caller can treat
// recurring and one-off events identically.
func TestExpandNilRuleYieldsSingleOccurrence(t *testing.T) {
	loc := denver(t)
	start := time.Date(2026, 8, 4, 19, 0, 0, 0, loc)
	got := Expand(start, start.Add(time.Hour), loc, nil, start.AddDate(0, 0, -1), start.AddDate(0, 0, 1))
	if len(got) != 1 || !got[0].Start.Equal(start) {
		t.Fatalf("got %d occurrences (%+v), want exactly the start", len(got), got)
	}
	// Outside the window it must not appear.
	if out := Expand(start, start.Add(time.Hour), loc, nil, start.AddDate(0, 0, 1), start.AddDate(0, 0, 2)); len(out) != 0 {
		t.Fatalf("occurrence leaked outside the window: %+v", out)
	}
}

func TestExpandRespectsUntilAndCount(t *testing.T) {
	loc := denver(t)
	start := time.Date(2026, 8, 4, 19, 0, 0, 0, loc)
	far := start.AddDate(1, 0, 0)

	counted := Expand(start, start.Add(time.Hour), loc, mustRule(t, "FREQ=WEEKLY;BYDAY=TU;COUNT=3"), start, far)
	if len(counted) != 3 {
		t.Fatalf("COUNT=3 produced %d occurrences", len(counted))
	}

	until := Expand(start, start.Add(time.Hour), loc,
		mustRule(t, "FREQ=WEEKLY;BYDAY=TU;UNTIL=20260901T000000Z"), start, far)
	if len(until) == 0 {
		t.Fatal("UNTIL produced no occurrences")
	}
	for _, occ := range until {
		if occ.Start.After(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)) {
			t.Errorf("%s is past UNTIL", occ.Start.Format(time.RFC3339))
		}
	}
}

func TestExpandIntervalSkipsWeeks(t *testing.T) {
	loc := denver(t)
	start := time.Date(2026, 8, 4, 19, 0, 0, 0, loc)
	got := Expand(start, start.Add(time.Hour), loc,
		mustRule(t, "FREQ=WEEKLY;INTERVAL=2;BYDAY=TU"), start, start.AddDate(0, 0, 42))
	for i := 1; i < len(got); i++ {
		if gap := got[i].Start.Sub(got[i-1].Start); gap < 13*24*time.Hour {
			t.Fatalf("INTERVAL=2 produced a %v gap between occurrences", gap)
		}
	}
}

// An unbounded rule must be stopped by the window, not run away.
func TestExpandUnboundedRuleIsBoundedByWindow(t *testing.T) {
	loc := denver(t)
	start := time.Date(2026, 1, 6, 19, 0, 0, 0, loc)
	got := Expand(start, start.Add(time.Hour), loc, mustRule(t, "FREQ=WEEKLY;BYDAY=TU"),
		start, start.AddDate(0, 0, 28))
	if len(got) != 4 {
		t.Fatalf("4-week window produced %d occurrences", len(got))
	}
}

func TestExpandMultipleDaysPerWeek(t *testing.T) {
	loc := denver(t)
	// Start on a Tuesday, recur Tuesday and Thursday.
	start := time.Date(2026, 8, 4, 19, 0, 0, 0, loc)
	got := Expand(start, start.Add(time.Hour), loc,
		mustRule(t, "FREQ=WEEKLY;BYDAY=TU,TH"), start, start.AddDate(0, 0, 14))
	if len(got) != 4 {
		t.Fatalf("expected 4 occurrences over 2 weeks, got %d", len(got))
	}
	for i := 1; i < len(got); i++ {
		if !got[i].Start.After(got[i-1].Start) {
			t.Fatalf("occurrences out of order: %v", got)
		}
	}
}

// The allowlist is what keeps Kit's view and Google's view of a series in
// agreement. Anything Expand cannot read must be refused on write.

func startsOn(occ []Occurrence, loc *time.Location) []string {
	out := make([]string, len(occ))
	for i, o := range occ {
		out[i] = o.Start.In(loc).Format("2006-01-02 15:04")
	}
	return out
}

func sameDates(t *testing.T, got []Occurrence, loc *time.Location, want ...string) {
	t.Helper()
	have := startsOn(got, loc)
	if len(have) != len(want) {
		t.Fatalf("expected %d occurrences %v, got %d: %v", len(want), want, len(have), have)
	}
	for i := range want {
		if have[i] != want[i] {
			t.Fatalf("occurrence %d: want %s, got %s (all: %v)", i, want[i], have[i], have)
		}
	}
}

// "First Friday" is the cadence a venue actually schedules on, and it lands on
// a different date every month -- which is exactly what a weekly rule cannot
// express and why this frequency exists.
func TestExpandMonthlyFirstFriday(t *testing.T) {
	loc := denver(t)
	// 2026-09-04 is the first Friday of September.
	start := time.Date(2026, 9, 4, 18, 0, 0, 0, loc)
	got := Expand(start, start.Add(4*time.Hour), loc, mustRule(t, "FREQ=MONTHLY;BYDAY=1FR"),
		start, time.Date(2027, 1, 1, 0, 0, 0, 0, loc))

	sameDates(t, got, loc,
		"2026-09-04 18:00",
		"2026-10-02 18:00",
		"2026-11-06 18:00",
		"2026-12-04 18:00",
	)
}

// A negative ordinal counts back from the end, so it moves between the 4th and
// 5th week depending on the month.
func TestExpandMonthlyLastFriday(t *testing.T) {
	loc := denver(t)
	start := time.Date(2026, 9, 25, 18, 0, 0, 0, loc) // last Friday of September
	got := Expand(start, start.Add(time.Hour), loc, mustRule(t, "FREQ=MONTHLY;BYDAY=-1FR"),
		start, time.Date(2027, 1, 1, 0, 0, 0, 0, loc))

	sameDates(t, got, loc,
		"2026-09-25 18:00",
		"2026-10-30 18:00",
		"2026-11-27 18:00",
		"2026-12-25 18:00",
	)
}

// RFC 5545 skips a month whose selector names no valid date rather than
// rolling into the next one. Getting this wrong is silent: time.Date would
// normalise February 31st into March 3rd and the series would drift.
func TestExpandMonthlySkipsMonthsWithoutTheDay(t *testing.T) {
	loc := denver(t)
	start := time.Date(2027, 1, 31, 12, 0, 0, 0, loc)
	got := Expand(start, start.Add(time.Hour), loc, mustRule(t, "FREQ=MONTHLY;BYMONTHDAY=31"),
		start, time.Date(2027, 7, 1, 0, 0, 0, 0, loc))

	// February, April and June have no 31st, so they are absent entirely.
	sameDates(t, got, loc,
		"2027-01-31 12:00",
		"2027-03-31 12:00",
		"2027-05-31 12:00",
	)
}

// The fifth Friday exists in some months and not others; it must be skipped,
// never clamped back to the fourth.
func TestExpandMonthlyFifthWeekdayIsSkippedNotClamped(t *testing.T) {
	loc := denver(t)
	start := time.Date(2027, 1, 29, 19, 0, 0, 0, loc) // 5th Friday of Jan 2027
	got := Expand(start, start.Add(time.Hour), loc, mustRule(t, "FREQ=MONTHLY;BYDAY=5FR"),
		start, time.Date(2027, 8, 1, 0, 0, 0, 0, loc))

	sameDates(t, got, loc,
		"2027-01-29 19:00",
		"2027-04-30 19:00",
		"2027-07-30 19:00",
	)
}

// -1 means the last day of the month whatever its number.
func TestExpandMonthlyNegativeMonthDay(t *testing.T) {
	loc := denver(t)
	start := time.Date(2027, 1, 31, 10, 0, 0, 0, loc)
	got := Expand(start, start.Add(time.Hour), loc, mustRule(t, "FREQ=MONTHLY;BYMONTHDAY=-1"),
		start, time.Date(2027, 5, 1, 0, 0, 0, 0, loc))

	sameDates(t, got, loc,
		"2027-01-31 10:00",
		"2027-02-28 10:00",
		"2027-03-31 10:00",
		"2027-04-30 10:00",
	)
}

// The same DST guarantee the weekly expander has: a monthly series crossing a
// transition keeps its wall-clock time, and would not if the expander advanced
// by a fixed duration.
func TestExpandMonthlyKeepsWallClockAcrossDST(t *testing.T) {
	loc := denver(t)
	// DST begins 2027-03-14. Second Sunday is the transition day itself, so
	// use the third Wednesday either side of it.
	start := time.Date(2027, 2, 17, 19, 0, 0, 0, loc)
	got := Expand(start, start.Add(time.Hour), loc, mustRule(t, "FREQ=MONTHLY;BYDAY=3WE"),
		start, time.Date(2027, 5, 1, 0, 0, 0, 0, loc))

	sameDates(t, got, loc,
		"2027-02-17 19:00",
		"2027-03-17 19:00",
		"2027-04-21 19:00",
	)
	for _, o := range got {
		if h := o.Start.In(loc).Hour(); h != 19 {
			t.Fatalf("occurrence %s drifted to %d:00", o.Start, h)
		}
	}
}

func TestExpandMonthlyIntervalAndAnchorDay(t *testing.T) {
	loc := denver(t)
	// No BYDAY or BYMONTHDAY: the series inherits DTSTART's day of the month.
	start := time.Date(2026, 9, 10, 17, 30, 0, 0, loc)
	got := Expand(start, start.Add(time.Hour), loc, mustRule(t, "FREQ=MONTHLY;INTERVAL=3"),
		start, time.Date(2027, 10, 1, 0, 0, 0, 0, loc))

	sameDates(t, got, loc,
		"2026-09-10 17:30",
		"2026-12-10 17:30",
		"2027-03-10 17:30",
		"2027-06-10 17:30",
		"2027-09-10 17:30",
	)
}

func TestExpandMonthlyRespectsCountAndUntil(t *testing.T) {
	loc := denver(t)
	start := time.Date(2026, 9, 4, 18, 0, 0, 0, loc)
	far := time.Date(2030, 1, 1, 0, 0, 0, 0, loc)

	counted := Expand(start, start.Add(time.Hour), loc,
		mustRule(t, "FREQ=MONTHLY;BYDAY=1FR;COUNT=3"), start, far)
	sameDates(t, counted, loc,
		"2026-09-04 18:00", "2026-10-02 18:00", "2026-11-06 18:00")

	until := Expand(start, start.Add(time.Hour), loc,
		mustRule(t, "FREQ=MONTHLY;BYDAY=1FR;UNTIL=20261101T000000Z"), start, far)
	sameDates(t, until, loc, "2026-09-04 18:00", "2026-10-02 18:00")
}

func TestRuleCoversMonthlyStart(t *testing.T) {
	loc := denver(t)
	first := time.Date(2026, 9, 4, 18, 0, 0, 0, loc) // first Friday
	third := time.Date(2026, 9, 18, 18, 0, 0, 0, loc)

	r := mustRule(t, "FREQ=MONTHLY;BYDAY=1FR")
	if !r.Covers(first) {
		t.Error("BYDAY=1FR should cover the first Friday")
	}
	if r.Covers(third) {
		t.Error("BYDAY=1FR should not cover the third Friday")
	}
	// No selector: any start is its own anchor day.
	if !mustRule(t, "FREQ=MONTHLY").Covers(third) {
		t.Error("a monthly rule with no selector should cover its own start")
	}
	if !mustRule(t, "FREQ=MONTHLY;BYMONTHDAY=18").Covers(third) {
		t.Error("BYMONTHDAY=18 should cover the 18th")
	}
}

// --- Explicit date lists ----------------------------------------------------

func TestSeriesExpandsExplicitDates(t *testing.T) {
	loc := denver(t)
	start := time.Date(2026, 9, 4, 18, 0, 0, 0, loc)
	s := Series{
		Start: start,
		End:   start.Add(2 * time.Hour),
		Loc:   loc,
		RDates: []time.Time{
			time.Date(2026, 11, 6, 18, 0, 0, 0, loc),
			time.Date(2026, 10, 2, 18, 0, 0, 0, loc),
		},
	}
	got := s.Expand(start, time.Date(2027, 1, 1, 0, 0, 0, 0, loc))

	// Out-of-order input comes back chronological, with DTSTART included.
	sameDates(t, got, loc,
		"2026-09-04 18:00", "2026-10-02 18:00", "2026-11-06 18:00")
	// Duration is preserved rather than the end wall-clock.
	for _, o := range got {
		if d := o.End.Sub(o.Start); d != 2*time.Hour {
			t.Fatalf("occurrence %s lasted %v, want 2h", o.Start, d)
		}
	}
}

func TestSeriesDedupesDatesAlreadyCoveredByTheRule(t *testing.T) {
	loc := denver(t)
	start := time.Date(2026, 9, 1, 19, 0, 0, 0, loc) // a Tuesday
	s := Series{
		Start: start,
		End:   start.Add(time.Hour),
		Loc:   loc,
		Rule:  mustRule(t, "FREQ=WEEKLY;BYDAY=TU"),
		// The 8th is already a Tuesday the rule covers; the 12th is not.
		RDates: []time.Time{
			time.Date(2026, 9, 8, 19, 0, 0, 0, loc),
			time.Date(2026, 9, 12, 19, 0, 0, 0, loc),
		},
	}
	got := s.Expand(start, time.Date(2026, 9, 20, 0, 0, 0, 0, loc))
	sameDates(t, got, loc,
		"2026-09-01 19:00",
		"2026-09-08 19:00", // once, not twice
		"2026-09-12 19:00",
		"2026-09-15 19:00",
	)
}

// COUNT and UNTIL bound the rule, never the explicit dates -- an explicit date
// is something a person typed, so a rule's end condition must not swallow it.
func TestSeriesExplicitDatesSurviveRuleCount(t *testing.T) {
	loc := denver(t)
	start := time.Date(2026, 9, 1, 19, 0, 0, 0, loc)
	s := Series{
		Start:  start,
		End:    start.Add(time.Hour),
		Loc:    loc,
		Rule:   mustRule(t, "FREQ=WEEKLY;BYDAY=TU;COUNT=2"),
		RDates: []time.Time{time.Date(2026, 12, 24, 19, 0, 0, 0, loc)},
	}
	got := s.Expand(start, time.Date(2027, 1, 1, 0, 0, 0, 0, loc))
	sameDates(t, got, loc,
		"2026-09-01 19:00", "2026-09-08 19:00", "2026-12-24 19:00")
}

func TestSeriesWindowsExplicitDates(t *testing.T) {
	loc := denver(t)
	start := time.Date(2026, 9, 4, 18, 0, 0, 0, loc)
	s := Series{
		Start: start,
		End:   start.Add(time.Hour),
		Loc:   loc,
		RDates: []time.Time{
			time.Date(2026, 10, 2, 18, 0, 0, 0, loc),
			time.Date(2026, 11, 6, 18, 0, 0, 0, loc),
		},
	}
	got := s.Expand(
		time.Date(2026, 10, 1, 0, 0, 0, 0, loc),
		time.Date(2026, 11, 1, 0, 0, 0, 0, loc),
	)
	sameDates(t, got, loc, "2026-10-02 18:00")
}
