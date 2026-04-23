package todo

import (
	"strings"
	"testing"
	"time"
)

func TestParseResolutionsWellFormed(t *testing.T) {
	input := `Sure, here you go:
[
  {"id":"r-1","kind":"task","label":"Email Bob","prompt":"Send an email to bob@example.com using send_email","shape":"once"},
  {"id":"r-2","kind":"task","label":"Weekly reminder","prompt":"Remind me to check on Bob","shape":"cron","cron":"0 9 * * 1"},
  {"id":"r-3","kind":"advice","label":"Call the CPA","body":"Ask your bookkeeper for a recommendation."}
]`
	out := parseResolutions(input)
	if len(out) != 3 {
		t.Fatalf("want 3 resolutions, got %d", len(out))
	}
	if out[0].Kind != ResolutionKindTask || out[0].Shape != "once" {
		t.Errorf("first entry wrong: %+v", out[0])
	}
	if out[1].Cron != "0 9 * * 1" || out[1].Shape != "cron" {
		t.Errorf("second entry wrong: %+v", out[1])
	}
	if out[2].Kind != ResolutionKindAdvice || out[2].Body == "" {
		t.Errorf("advice entry wrong: %+v", out[2])
	}
}

func TestParseResolutionsAdviceStripsExecutableFields(t *testing.T) {
	// Even if the model slips and includes a prompt/shape on an advice
	// entry, the parser must strip them — a stray shape could later
	// confuse the chip filter or accept handler.
	input := `[{"kind":"advice","label":"x","prompt":"p","shape":"once","cron":"0 9 * * 1"}]`
	out := parseResolutions(input)
	if len(out) != 1 {
		t.Fatalf("want 1, got %d", len(out))
	}
	if out[0].Prompt != "" || out[0].Shape != "" || out[0].Cron != "" {
		t.Errorf("advice should strip executable fields: %+v", out[0])
	}
}

func TestParseResolutionsInfersKindFromShape(t *testing.T) {
	// A well-formed entry missing `kind` but with prompt + shape should
	// infer kind=task, matching the model's likely omission pattern.
	input := `[{"label":"x","prompt":"y","shape":"once"}]`
	out := parseResolutions(input)
	if len(out) != 1 {
		t.Fatalf("want 1, got %d", len(out))
	}
	if out[0].Kind != ResolutionKindTask {
		t.Errorf("want task, got %q", out[0].Kind)
	}
}

func TestParseResolutionsInfersAdviceWhenNoPrompt(t *testing.T) {
	input := `[{"label":"Call a lawyer"}]`
	out := parseResolutions(input)
	if len(out) != 1 {
		t.Fatalf("want 1, got %d", len(out))
	}
	if out[0].Kind != ResolutionKindAdvice {
		t.Errorf("want advice, got %q", out[0].Kind)
	}
}

func TestParseResolutionsDropsBadCron(t *testing.T) {
	input := `[
  {"kind":"task","label":"Good once","prompt":"do the thing","shape":"once"},
  {"kind":"task","label":"Bad cron","prompt":"do on schedule","shape":"cron","cron":"not a cron"},
  {"kind":"task","label":"Missing cron","prompt":"do on schedule","shape":"cron"}
]`
	out := parseResolutions(input)
	if len(out) != 1 {
		t.Fatalf("want 1 resolution, got %d: %+v", len(out), out)
	}
	if out[0].Label != "Good once" {
		t.Errorf("wrong surviving entry: %+v", out[0])
	}
}

func TestParseResolutionsTaskMissingPrompt(t *testing.T) {
	// An explicit task kind without a prompt is invalid — drop it.
	input := `[{"kind":"task","label":"x","shape":"once"}]`
	out := parseResolutions(input)
	if len(out) != 0 {
		t.Errorf("want 0, got %+v", out)
	}
}

func TestParseResolutionsUnknownKindDropped(t *testing.T) {
	input := `[{"kind":"magic","label":"x","prompt":"y","shape":"once"}]`
	out := parseResolutions(input)
	if len(out) != 0 {
		t.Errorf("want 0, got %+v", out)
	}
}

func TestParseResolutionsMissingLabel(t *testing.T) {
	input := `[{"kind":"advice","label":""}]`
	out := parseResolutions(input)
	if len(out) != 0 {
		t.Errorf("want 0, got %+v", out)
	}
}

func TestParseResolutionsAssignsMissingIDs(t *testing.T) {
	input := `[{"kind":"advice","label":"x"}]`
	out := parseResolutions(input)
	if len(out) != 1 {
		t.Fatalf("want 1, got %d", len(out))
	}
	if !strings.HasPrefix(out[0].ID, "r-") {
		t.Errorf("id should start with r-, got %q", out[0].ID)
	}
}

func TestParseResolutionsCapsAtMax(t *testing.T) {
	input := `[
  {"kind":"advice","label":"a"},
  {"kind":"advice","label":"b"},
  {"kind":"advice","label":"c"},
  {"kind":"advice","label":"d"},
  {"kind":"advice","label":"e"}
]`
	out := parseResolutions(input)
	if len(out) != maxResolutions {
		t.Errorf("want %d, got %d", maxResolutions, len(out))
	}
}

func TestParseResolutionsEmptyArray(t *testing.T) {
	out := parseResolutions(`[]`)
	if out == nil {
		t.Fatal("want non-nil empty slice")
	}
	if len(out) != 0 {
		t.Errorf("want empty, got %+v", out)
	}
}

func TestParseResolutionsGarbage(t *testing.T) {
	for _, s := range []string{
		"not json at all",
		"",
		`{"not":"an array"}`,
		`[{bad json]`,
	} {
		out := parseResolutions(s)
		if out == nil {
			t.Errorf("input %q: want non-nil empty slice", s)
			continue
		}
		if len(out) != 0 {
			t.Errorf("input %q: want empty, got %+v", s, out)
		}
	}
}

func TestParseResolutionsTaskOnceWithCronIgnored(t *testing.T) {
	// A task with shape=once should clear any stray Cron field so the
	// task-create path never receives conflicting instructions.
	input := `[{"kind":"task","label":"x","prompt":"y","shape":"once","cron":"0 9 * * 1"}]`
	out := parseResolutions(input)
	if len(out) != 1 {
		t.Fatalf("want 1, got %d", len(out))
	}
	if out[0].Cron != "" {
		t.Errorf("once shape should clear cron, got %q", out[0].Cron)
	}
}

func TestSnoozeUntilAtAdvancesDateAndSetsThreeAM(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("load tz: %v", err)
	}
	// Snoozing at 2pm Monday for 1 day should return Tuesday 3am in LA
	// (tomorrow's date + 03:00 local).
	now := time.Date(2026, 4, 20, 14, 0, 0, 0, loc) // Mon 14:00 PDT
	got, err := snoozeUntilAt(now, 1, "America/Los_Angeles")
	if err != nil {
		t.Fatalf("snoozeUntilAt: %v", err)
	}
	local := got.In(loc)
	if local.Year() != 2026 || local.Month() != 4 || local.Day() != 21 {
		t.Errorf("expected date 2026-04-21 PDT, got %s", local)
	}
	if local.Hour() != 3 || local.Minute() != 0 {
		t.Errorf("expected 03:00 PDT, got %02d:%02d", local.Hour(), local.Minute())
	}
}

func TestSnoozeUntilAtBeforeSnoozeHourStillAdvances(t *testing.T) {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatalf("load tz: %v", err)
	}
	// Snoozing at 1am should still push to TOMORROW 3am, not today 3am
	// — "1 day" means a full calendar-day skip regardless of current
	// clock time.
	now := time.Date(2026, 4, 20, 1, 0, 0, 0, loc)
	got, err := snoozeUntilAt(now, 1, "America/Los_Angeles")
	if err != nil {
		t.Fatalf("snoozeUntilAt: %v", err)
	}
	local := got.In(loc)
	if local.Day() != 21 || local.Hour() != 3 {
		t.Errorf("expected Apr 21 03:00 PDT, got %s", local)
	}
}

func TestSnoozeUntilAtSevenDays(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 4, 20, 10, 0, 0, 0, loc)
	got, err := snoozeUntilAt(now, 7, "America/New_York")
	if err != nil {
		t.Fatalf("snoozeUntilAt: %v", err)
	}
	local := got.In(loc)
	if local.Day() != 27 || local.Hour() != 3 {
		t.Errorf("expected Apr 27 03:00 EDT, got %s", local)
	}
}

func TestSnoozeUntilAtUnknownTimezoneErrors(t *testing.T) {
	_, err := snoozeUntilAt(time.Now(), 1, "Not/A/Zone")
	if err == nil {
		t.Fatal("expected error for bad timezone")
	}
}

func TestSnoozeUntilNextMondayFromWednesday(t *testing.T) {
	loc, _ := time.LoadLocation("America/Los_Angeles")
	// Wed 2026-04-22 14:00 PDT → next Monday 2026-04-27 03:00 PDT.
	now := time.Date(2026, 4, 22, 14, 0, 0, 0, loc)
	got, err := snoozeUntilNextMondayAt(now, "America/Los_Angeles")
	if err != nil {
		t.Fatalf("snoozeUntilNextMondayAt: %v", err)
	}
	local := got.In(loc)
	if local.Weekday() != time.Monday {
		t.Errorf("expected Monday, got %s", local.Weekday())
	}
	if local.Day() != 27 || local.Hour() != 3 {
		t.Errorf("expected 2026-04-27 03:00 PDT, got %s", local)
	}
}

func TestSnoozeUntilNextMondayFromMondayGoesAWeekOut(t *testing.T) {
	loc, _ := time.LoadLocation("America/Los_Angeles")
	// Tapping "Monday" when today is Monday should land a full week
	// out — the user sees today and means the next one.
	now := time.Date(2026, 4, 20, 14, 0, 0, 0, loc)
	got, err := snoozeUntilNextMondayAt(now, "America/Los_Angeles")
	if err != nil {
		t.Fatalf("snoozeUntilNextMondayAt: %v", err)
	}
	local := got.In(loc)
	if local.Weekday() != time.Monday {
		t.Errorf("expected Monday, got %s", local.Weekday())
	}
	if local.Day() != 27 {
		t.Errorf("expected Apr 27 (next Monday), got %s", local)
	}
}

func TestSnoozeUntilNextMondayFromSunday(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	// Sun 2026-04-19 → Mon 2026-04-20 (one day out).
	now := time.Date(2026, 4, 19, 10, 0, 0, 0, loc)
	got, err := snoozeUntilNextMondayAt(now, "America/New_York")
	if err != nil {
		t.Fatalf("snoozeUntilNextMondayAt: %v", err)
	}
	local := got.In(loc)
	if local.Day() != 20 || local.Hour() != 3 {
		t.Errorf("expected 2026-04-20 03:00 EDT, got %s", local)
	}
}

func TestSnoozeUntilNextMondayBadTimezone(t *testing.T) {
	if _, err := snoozeUntilNextMondayAt(time.Now(), "Not/A/Zone"); err == nil {
		t.Fatal("expected error for bad timezone")
	}
}

func TestSnoozeDaysToUntilRejectsInvalidDays(t *testing.T) {
	for _, d := range []int{0, 2, 4, 8, -1} {
		if _, err := SnoozeDaysToUntil(d, "UTC"); err == nil {
			t.Errorf("expected error for days=%d", d)
		}
	}
	for _, d := range []int{1, 3, 7} {
		if _, err := SnoozeDaysToUntil(d, "UTC"); err != nil {
			t.Errorf("days=%d should succeed, got %v", d, err)
		}
	}
}
