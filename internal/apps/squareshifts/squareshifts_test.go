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
		ShiftID:  "SHIFT123",
		StartAt:  "2026-07-15T09:00:00-06:00",
		EndAt:    "2026-07-15T17:00:00-06:00",
		Timezone: "America/Denver",
		Member:   "Alice Ng",
		Location: "Front counter",
		Notes:    "cover for Sam",
	}
	ev := buildEvent(shift, tenant)

	if ev.Summary != "Alice Ng" {
		t.Fatalf("summary = %q", ev.Summary)
	}
	if ev.Location != "Front counter" {
		t.Fatalf("location = %q", ev.Location)
	}
	if ev.Start == nil || ev.Start.DateTime != shift.StartAt || ev.Start.TimeZone != "America/Denver" {
		t.Fatalf("start = %+v", ev.Start)
	}
	priv := ev.ExtendedProperties.Private
	if priv["squareShiftId"] != "SHIFT123" || priv["source"] != "square" || priv["kitTenantId"] != tenant.String() {
		t.Fatalf("private props = %+v", priv)
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
	moved.EndAt = "2026-07-15T18:00:00Z"
	if contentHash(buildEvent(moved, tenant)) == h1 {
		t.Fatal("hash unchanged after end time changed")
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
	end := start.AddDate(0, 0, 21)
	inWindow := googlecalendar.Event{Start: &googlecalendar.EventDateTime{DateTime: "2026-07-15T09:00:00-06:00"}}
	past := googlecalendar.Event{Start: &googlecalendar.EventDateTime{DateTime: "2026-07-01T09:00:00Z"}}
	future := googlecalendar.Event{Start: &googlecalendar.EventDateTime{DateTime: "2026-09-01T09:00:00Z"}}
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
