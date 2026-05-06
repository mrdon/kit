package cards

import (
	"testing"
	"time"
)

func TestCardSnoozeOneMonthAtFromWednesday(t *testing.T) {
	loc, _ := time.LoadLocation("America/Los_Angeles")
	// Wed 2026-04-22 → +30d = Fri 2026-05-22 → next Mon 2026-05-25 07:00 PDT.
	now := time.Date(2026, 4, 22, 14, 0, 0, 0, loc)
	got, err := cardSnoozeOneMonthAt(now, "America/Los_Angeles")
	if err != nil {
		t.Fatalf("cardSnoozeOneMonthAt: %v", err)
	}
	local := got.In(loc)
	if local.Weekday() != time.Monday {
		t.Errorf("expected Monday, got %s", local.Weekday())
	}
	if local.Year() != 2026 || local.Month() != time.May || local.Day() != 25 || local.Hour() != 7 {
		t.Errorf("expected 2026-05-25 07:00 PDT, got %s", local)
	}
}

func TestCardSnoozeOneMonthAtKeepsMondayWhen30dLandsOnMonday(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	// Sat 2026-04-25 + 30d = Mon 2026-05-25; do not skip ahead a week.
	now := time.Date(2026, 4, 25, 9, 0, 0, 0, loc)
	got, err := cardSnoozeOneMonthAt(now, "America/New_York")
	if err != nil {
		t.Fatalf("cardSnoozeOneMonthAt: %v", err)
	}
	local := got.In(loc)
	if local.Day() != 25 || local.Month() != time.May || local.Hour() != 7 {
		t.Errorf("expected 2026-05-25 07:00 EDT, got %s", local)
	}
}

func TestCardSnoozeOneMonthAtBadTimezone(t *testing.T) {
	if _, err := cardSnoozeOneMonthAt(time.Now(), "Not/A/Zone"); err == nil {
		t.Fatal("expected error for bad timezone")
	}
}
