package events

import (
	"errors"
	"testing"
	"time"
)

func denver(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Fatalf("loading America/Denver: %v", err)
	}
	return loc
}

func mustRule(t *testing.T, s string) *Rule {
	t.Helper()
	r, err := ParseRule(s)
	if err != nil {
		t.Fatalf("ParseRule(%q): %v", s, err)
	}
	return r
}

// The highest-value test in the app. Adding 7*24h instead of advancing
// calendar days silently moves 7pm trivia to 6pm for half the year, and every
// other test in this file would still pass.
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
func TestParseRuleRejectsUnsupported(t *testing.T) {
	for _, s := range []string{
		"FREQ=MONTHLY;BYDAY=-1FR",
		"FREQ=DAILY",
		"FREQ=YEARLY;BYMONTH=3",
		"FREQ=WEEKLY;BYSETPOS=-1",
		"FREQ=WEEKLY;BYMONTHDAY=15",
		"FREQ=WEEKLY;BYDAY=XX",
		"FREQ=WEEKLY;BYDAY=",
		"FREQ=WEEKLY;INTERVAL=0",
		"FREQ=WEEKLY;INTERVAL=-1",
		"FREQ=WEEKLY;COUNT=0",
		"FREQ=WEEKLY;UNTIL=next-tuesday",
		"FREQ=WEEKLY;UNTIL=20261231T065959", // floating, no zone
		"FREQ=WEEKLY;COUNT=5;UNTIL=20261231T065959Z",
		"BYDAY=TU", // no FREQ
		"garbage",
	} {
		if _, err := ParseRule(s); err == nil {
			t.Errorf("ParseRule(%q) accepted an unsupported rule", s)
		} else if !errors.Is(err, ErrUnsupportedRule) {
			t.Errorf("ParseRule(%q) error %v does not wrap ErrUnsupportedRule", s, err)
		}
	}
}

func TestParseRuleAcceptsSupported(t *testing.T) {
	for _, s := range []string{
		"FREQ=WEEKLY",
		"FREQ=WEEKLY;BYDAY=TU",
		"RRULE:FREQ=WEEKLY;BYDAY=TU",
		"freq=weekly;byday=tu",
		"FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,WE,FR",
		"FREQ=WEEKLY;BYDAY=TU;UNTIL=20261231T065959Z",
		"FREQ=WEEKLY;BYDAY=TU;UNTIL=20261231",
		"FREQ=WEEKLY;BYDAY=TU;COUNT=10",
	} {
		if _, err := ParseRule(s); err != nil {
			t.Errorf("ParseRule(%q) rejected a supported rule: %v", s, err)
		}
	}
	if r, err := ParseRule(""); err != nil || r != nil {
		t.Fatalf("empty rule should parse to (nil, nil), got (%v, %v)", r, err)
	}
}

// The stored form must round-trip, since it is both rendered to Google and fed
// into the sync's content hash.
func TestRuleStringRoundTrips(t *testing.T) {
	for _, s := range []string{
		"FREQ=WEEKLY",
		"FREQ=WEEKLY;BYDAY=TU",
		"FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,WE,FR",
		"FREQ=WEEKLY;BYDAY=TU;UNTIL=20261231T065959Z",
		"FREQ=WEEKLY;BYDAY=TU;COUNT=10",
	} {
		r := mustRule(t, s)
		if got := r.String(); got != s {
			t.Errorf("round trip: %q -> %q", s, got)
		}
		if _, err := ParseRule(r.String()); err != nil {
			t.Errorf("re-parsing %q failed: %v", r.String(), err)
		}
	}
}

func TestRuleCoversWeekday(t *testing.T) {
	if !mustRule(t, "FREQ=WEEKLY").CoversWeekday(time.Monday) {
		t.Error("a rule with no BYDAY should accept the start's own weekday")
	}
	r := mustRule(t, "FREQ=WEEKLY;BYDAY=TU,TH")
	if !r.CoversWeekday(time.Tuesday) || !r.CoversWeekday(time.Thursday) {
		t.Error("BYDAY=TU,TH should cover Tuesday and Thursday")
	}
	if r.CoversWeekday(time.Monday) {
		t.Error("BYDAY=TU,TH should not cover Monday")
	}
	var nilRule *Rule
	if nilRule.CoversWeekday(time.Monday) {
		t.Error("nil rule covers nothing")
	}
}
