package events

import (
	"errors"
	"strings"
	"testing"
)

func TestClosedDaySkipsRepeatingEventsAndFlagsOneOffs(t *testing.T) {
	f := newFixture(t)
	weekly, day := weeklyFixture(t, f)
	f.create(t, CreateParams{Title: "Holiday Party", StartsAt: day.Format(skipDayLayout) + " 17:00"})

	res, err := f.svc.SetSpecialHours(f.ctx, f.tenant.ID, SpecialHours{Day: day, Label: "Closed", Closed: true})
	if err != nil {
		t.Fatalf("SetSpecialHours: %v", err)
	}
	if len(res.Skipped) != 1 || res.Skipped[0] != weekly.Title {
		t.Fatalf("skipped = %v, want [%s]", res.Skipped, weekly.Title)
	}
	if len(res.Conflicts) != 1 || !strings.Contains(res.Conflicts[0], "Holiday Party") {
		t.Fatalf("conflicts = %v, want the one-off flagged", res.Conflicts)
	}
	got, err := f.svc.Get(f.ctx, f.tenant.ID, weekly.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if hasOccurrenceOn(got, day) {
		t.Fatal("the repeating event still happens on the closed day")
	}

	// Running it again is a no-op, not a second skip or an error.
	again, err := f.svc.SetSpecialHours(f.ctx, f.tenant.ID, SpecialHours{Day: day, Closed: true})
	if err != nil {
		t.Fatalf("second SetSpecialHours: %v", err)
	}
	if len(again.Skipped) != 0 {
		t.Fatalf("second run skipped %v again", again.Skipped)
	}
}

// A short day reports an event that overruns it, and leaves it alone.
func TestShortDayFlagsButDoesNotSkip(t *testing.T) {
	f := newFixture(t)
	weekly, day := weeklyFixture(t, f) // 19:00-21:00
	res, err := f.svc.SetSpecialHours(f.ctx, f.tenant.ID, SpecialHours{
		Day: day, Label: "Christmas Eve", Opens: "12:00", Closes: "19:00",
	})
	if err != nil {
		t.Fatalf("SetSpecialHours: %v", err)
	}
	if len(res.Skipped) != 0 {
		t.Fatalf("a short day skipped %v", res.Skipped)
	}
	if len(res.Conflicts) != 1 || !strings.Contains(res.Conflicts[0], weekly.Title) {
		t.Fatalf("conflicts = %v, want the overrunning event", res.Conflicts)
	}
	got, _ := f.svc.Get(f.ctx, f.tenant.ID, weekly.ID)
	if !hasOccurrenceOn(got, day) {
		t.Fatal("a short day removed the event")
	}
}

func TestHoursValidation(t *testing.T) {
	f := newFixture(t)
	_, day := weeklyFixture(t, f)
	for _, h := range []SpecialHours{
		{Day: day, Opens: "19:00", Closes: "12:00"},
		{Day: day, Opens: "noon", Closes: "19:00"},
		{Day: day},
	} {
		if _, err := f.svc.SetSpecialHours(f.ctx, f.tenant.ID, h); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: err = %v, want ErrInvalid", h, err)
		}
	}
}

func TestFeedCarriesHolidayHours(t *testing.T) {
	f := newFixture(t)
	_, day := weeklyFixture(t, f)
	if _, err := f.svc.SetSpecialHours(f.ctx, f.tenant.ID, SpecialHours{
		Day: day, Label: "Christmas Eve", Opens: "12:00", Closes: "19:00", Note: "No trivia",
	}); err != nil {
		t.Fatalf("SetSpecialHours: %v", err)
	}
	feed, err := f.svc.BuildFeed(f.ctx, f.tenant.ID, "")
	if err != nil {
		t.Fatalf("BuildFeed: %v", err)
	}
	want := FeedSpecialHours{Date: day.Format(skipDayLayout), Label: "Christmas Eve", Opens: "12:00", Closes: "19:00", Note: "No trivia"}
	if len(feed.SpecialHours) != 1 || feed.SpecialHours[0] != want {
		t.Fatalf("special_hours = %+v, want [%+v]", feed.SpecialHours, want)
	}
	removed, err := f.svc.RemoveSpecialHours(f.ctx, f.tenant.ID, day)
	if err != nil || !removed {
		t.Fatalf("RemoveSpecialHours = %v, %v", removed, err)
	}
}
