package squareshifts

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/googlecalendar"
	"github.com/mrdon/kit/internal/apps/square"
)

func TestBuildEventStampsAndID(t *testing.T) {
	tenant := uuid.New()
	shift := square.EnrichedShift{
		ShiftID:     "SHIFT123",
		StartAt:     "2026-07-15T09:00:00-06:00",
		EndAt:       "2026-07-15T17:00:00-06:00",
		Timezone:    "America/Denver",
		Member:      "Alice Ng",
		MemberFirst: "Alice",
		Location:    "Front counter",
		Notes:       "cover for Sam",
	}
	ev := buildEvent(shift, tenant)

	// Informal first name + shift hours for opener/closer context.
	if ev.Summary != "Alice · 9:00am–5:00pm" {
		t.Fatalf("summary = %q", ev.Summary)
	}
	if ev.Location != "Front counter" {
		t.Fatalf("location = %q", ev.Location)
	}
	// All-day event: Start.Date is the shift's local date, End.Date the next
	// day (exclusive); no DateTime.
	if ev.Start == nil || ev.Start.Date != "2026-07-15" || ev.Start.DateTime != "" {
		t.Fatalf("start = %+v", ev.Start)
	}
	if ev.End == nil || ev.End.Date != "2026-07-16" {
		t.Fatalf("end = %+v", ev.End)
	}
	priv := ev.ExtendedProperties.Private
	if priv["squareShiftId"] != "SHIFT123" || priv["source"] != "square" {
		t.Fatalf("private props = %+v", priv)
	}
	// Ownership stamp: app + tenant. The reconcile sweep filters on exactly
	// this pair, so a missing kitApp would let it claim other features'
	// events on a shared calendar.
	if priv[googlecalendar.PropApp] != AppName || priv[googlecalendar.PropTenant] != tenant.String() {
		t.Fatalf("ownership stamp = %+v", priv)
	}
	// Deterministic id: same shift → same id across builds.
	if buildEvent(shift, tenant).ID != ev.ID {
		t.Fatal("event id not deterministic")
	}
}

func TestContentHashChangesWithFields(t *testing.T) {
	tenant := uuid.New()
	base := square.EnrichedShift{ShiftID: "S1", StartAt: "2026-07-15T09:00:00Z", EndAt: "2026-07-15T17:00:00Z", Timezone: "UTC", Member: "A"}
	h1 := contentHash(buildEvent(base, tenant))

	moved := base
	moved.StartAt = "2026-07-16T09:00:00Z"
	if contentHash(buildEvent(moved, tenant)) == h1 {
		t.Fatal("hash unchanged after shift date changed")
	}

	renamed := base
	renamed.Member = "B"
	if contentHash(buildEvent(renamed, tenant)) == h1 {
		t.Fatal("hash unchanged after member changed")
	}

	// Same inputs → same hash.
	if contentHash(buildEvent(base, tenant)) != h1 {
		t.Fatal("hash not stable for identical shift")
	}
}

func TestFormatSummary(t *testing.T) {
	got := formatSummary(SyncSummary{Created: 2, Updated: 1, Deleted: 3})
	want := "Sync complete: 2 created, 1 updated, 3 deleted."
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestEventStartsInWindow(t *testing.T) {
	start := time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, syncWindowMonths, 0)
	inWindow := googlecalendar.Event{Start: &googlecalendar.EventDateTime{DateTime: "2026-07-15T09:00:00-06:00"}}
	past := googlecalendar.Event{Start: &googlecalendar.EventDateTime{DateTime: "2026-07-01T09:00:00Z"}}
	future := googlecalendar.Event{Start: &googlecalendar.EventDateTime{DateTime: "2026-11-01T09:00:00Z"}}
	noStart := googlecalendar.Event{}

	if !eventStartsInWindow(inWindow, start, end) {
		t.Error("in-window event should match")
	}
	if eventStartsInWindow(past, start, end) {
		t.Error("past event must not be treated as orphan")
	}
	if eventStartsInWindow(future, start, end) {
		t.Error("beyond-window event should not match")
	}
	if eventStartsInWindow(noStart, start, end) {
		t.Error("event with no start must not be deletable")
	}
}

// The sync writes all-day events (Start.Date, no DateTime). A window check
// that only understood DateTime silently spared every real event from the
// orphan sweep, so cover the all-day shape explicitly.
func TestEventStartsInWindowAllDay(t *testing.T) {
	start := time.Date(2026, 7, 12, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, syncWindowMonths, 0)

	cases := []struct {
		name string
		date string
		want bool
	}{
		{"first day of window", "2026-07-12", true},
		{"mid window", "2026-08-03", true},
		{"day before window", "2026-07-11", false},
		{"last day in window", "2026-09-11", true},
		{"first day past window", "2026-09-12", false},
		{"unparseable", "not-a-date", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := googlecalendar.Event{Start: &googlecalendar.EventDateTime{Date: c.date}}
			if got := eventStartsInWindow(e, start, end); got != c.want {
				t.Errorf("date %q: got %v want %v", c.date, got, c.want)
			}
		})
	}

	// An event built by the real sync must be visible to the sweep — this is
	// the regression that mattered.
	ev := buildEvent(square.EnrichedShift{
		ShiftID: "S1", StartAt: "2026-07-15T09:00:00-06:00", EndAt: "2026-07-15T17:00:00-06:00", Member: "A",
	}, uuid.New())
	if !eventStartsInWindow(*ev, start, end) {
		t.Fatalf("sync-authored all-day event not seen as in-window: start = %+v", ev.Start)
	}
}

func TestPrivateProp(t *testing.T) {
	e := googlecalendar.Event{ExtendedProperties: &googlecalendar.ExtendedProperties{
		Private: map[string]string{"squareShiftId": "S9"},
	}}
	if privateProp(e, "squareShiftId") != "S9" {
		t.Error("expected S9")
	}
	if privateProp(e, "missing") != "" {
		t.Error("absent key should be empty")
	}
	if privateProp(googlecalendar.Event{}, "squareShiftId") != "" {
		t.Error("nil extended properties should be empty")
	}
}
