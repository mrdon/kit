package events

import (
	"testing"
	"time"
)

// An all-day event is stored as midnight, so "still ahead" measured from
// the current instant drops it the moment its own day begins -- a Halloween
// party vanished from the website at 1:45am on Halloween, and the next event
// that day headlined instead. The feed's window opens at the start of today
// in the venue's zone, so everything on today stays on the page all day.
//
// Frozen clock rather than a relative date: the test is about where one
// specific day starts, which is a property of the calendar, not of "now".
func TestFeedKeepsTodaysAllDayEvent(t *testing.T) {
	sf := newSyncFixture(t)
	loc, err := time.LoadLocation(DefaultTimezone)
	if err != nil {
		t.Fatalf("load zone: %v", err)
	}
	day := time.Date(2026, 10, 31, 0, 0, 0, 0, loc)
	prev := timeNow
	timeNow = func() time.Time { return day.Add(105 * time.Minute) }
	t.Cleanup(func() { timeNow = prev })

	party := sf.create(t, CreateParams{
		Title: "Halloween Party", StartsAt: day.Format("2006-01-02"), AllDay: true,
		Visibility: VisibilityPublic,
	})
	sf.publish(t, party)
	yesterday := sf.create(t, CreateParams{
		Title: "Yesterday's Party", StartsAt: day.AddDate(0, 0, -1).Format("2006-01-02"),
		AllDay: true, Visibility: VisibilityPublic,
	})
	sf.publish(t, yesterday)

	got := sf.feed(t).titles()
	if len(got) != 1 || got[0] != "Halloween Party" {
		t.Fatalf("feed = %v, want only today's all-day event", got)
	}

	// And it goes once the day is over, not before and not a day late.
	timeNow = func() time.Time { return day.AddDate(0, 0, 1).Add(15 * time.Minute) }
	if got := sf.feed(t).titles(); len(got) != 0 {
		t.Fatalf("feed = %v after the day ended, want nothing", got)
	}
}

// A timed event stays listed until its end, not its start: a show that
// started at 11pm is still on at 12:30am, and one that ended at midnight is
// over.
func TestFeedKeepsATimedEventUntilItEnds(t *testing.T) {
	sf := newSyncFixture(t)
	loc, err := time.LoadLocation(DefaultTimezone)
	if err != nil {
		t.Fatalf("load zone: %v", err)
	}
	day := time.Date(2026, 10, 30, 0, 0, 0, 0, loc)
	prev := timeNow
	timeNow = func() time.Time { return day.Add(30 * time.Minute) }
	t.Cleanup(func() { timeNow = prev })

	const layout = "2006-01-02 15:04"
	on := sf.create(t, CreateParams{
		Title: "Late Show", Visibility: VisibilityPublic,
		StartsAt: day.Add(-time.Hour).Format(layout), EndsAt: day.Add(2 * time.Hour).Format(layout),
	})
	sf.publish(t, on)
	over := sf.create(t, CreateParams{
		Title: "Early Show", Visibility: VisibilityPublic,
		StartsAt: day.Add(-3 * time.Hour).Format(layout), EndsAt: day.Format(layout),
	})
	sf.publish(t, over)

	got := sf.feed(t).titles()
	if len(got) != 1 || got[0] != "Late Show" {
		t.Fatalf("feed = %v, want only the event still running", got)
	}
}
