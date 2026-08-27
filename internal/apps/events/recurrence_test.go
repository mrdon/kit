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

func TestParseRuleRejectsUnsupported(t *testing.T) {
	for _, s := range []string{
		"FREQ=DAILY",
		"FREQ=YEARLY;BYMONTH=3",
		"FREQ=WEEKLY;BYSETPOS=-1",
		"FREQ=WEEKLY;BYMONTHDAY=15",
		"FREQ=WEEKLY;BYDAY=XX",
		"FREQ=MONTHLY;BYDAY=0FR", // a zeroth Friday is not a thing
		"FREQ=MONTHLY;BYDAY=6FR", // nor a sixth
		"FREQ=MONTHLY;BYMONTHDAY=0",
		"FREQ=MONTHLY;BYMONTHDAY=32",
		"FREQ=MONTHLY;BYDAY=1FR;BYMONTHDAY=13", // intersection we do not expand
		"FREQ=WEEKLY;BYDAY=TU;BYDAY=WE",        // duplicate key
		"FREQ=MONTHLY;BYSETPOS=-1",
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
		"FREQ=MONTHLY",
		"FREQ=MONTHLY;BYDAY=1FR",
		"FREQ=MONTHLY;BYDAY=-1FR",
		"FREQ=MONTHLY;BYDAY=FR",
		"FREQ=MONTHLY;BYMONTHDAY=15",
		"FREQ=MONTHLY;BYMONTHDAY=-1",
		"FREQ=MONTHLY;INTERVAL=3;BYMONTHDAY=1,15",
		"freq=monthly;byday=1fr",
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
		"FREQ=MONTHLY",
		"FREQ=MONTHLY;BYDAY=1FR",
		"FREQ=MONTHLY;BYDAY=-1FR",
		"FREQ=MONTHLY;BYDAY=FR",
		"FREQ=MONTHLY;BYMONTHDAY=15",
		"FREQ=MONTHLY;BYMONTHDAY=-1",
		"FREQ=MONTHLY;INTERVAL=3;BYMONTHDAY=1,15",
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

// --- Monthly recurrence -----------------------------------------------------

// startsOn is a compact assertion helper: the local dates the expansion fell
// on, so a test reads as the calendar a person would check it against.
