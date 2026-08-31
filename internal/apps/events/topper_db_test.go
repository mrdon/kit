package events

import (
	"strings"
	"testing"
	"time"
)

// The end-to-end assembly, through the real query rather than a slice of
// structs. The query's window is the part worth exercising: a weekly series
// whose starts_at is months behind still has to reach the card, and everything
// that is not published-and-public still has to be kept off it.
func TestBuildTopperFromDatabase(t *testing.T) {
	sf := newSyncFixture(t)
	loc := denver(t)

	// The week of Sunday 13 September 2026.
	week := time.Date(2026, 9, 15, 12, 0, 0, 0, loc)

	trivia := sf.create(t, CreateParams{
		Title:      "Trivia Night",
		Summary:    "Quiz night every Wednesday. Free to play.",
		StartsAt:   "2026-01-07 18:30",
		RRule:      "FREQ=WEEKLY;BYDAY=WE",
		Visibility: VisibilityPublic,
	})
	sf.publish(t, trivia)

	release := sf.create(t, CreateParams{
		Title:       "Beer Release",
		Description: "New hazy IPA\nGlassware giveaway",
		StartsAt:    "2026-09-18 17:00",
		Visibility:  VisibilityPublic,
	})
	sf.publish(t, release)

	// Published but private: a booking that must never print on a table.
	booking := sf.create(t, CreateParams{
		Title:      "Sarah's 40th",
		StartsAt:   "2026-09-19 18:00",
		Visibility: VisibilityPrivate,
	})
	sf.publish(t, booking)

	// Public but still a draft.
	sf.create(t, CreateParams{
		Title:      "Maybe A Quiz",
		StartsAt:   "2026-09-17 19:00",
		Visibility: VisibilityPublic,
	})

	// Public, published, and in a different week.
	nextMonth := sf.create(t, CreateParams{
		Title:      "October Fest",
		StartsAt:   "2026-10-10 12:00",
		Visibility: VisibilityPublic,
	})
	sf.publish(t, nextMonth)

	topper, err := sf.app.buildTopper(sf.ctx, sf.tenant, week)
	if err != nil {
		t.Fatalf("buildTopper: %v", err)
	}

	var got []string
	for _, r := range topper.Rows {
		got = append(got, r.Day+" "+r.Title)
	}
	if want := "WED Trivia Night,FRI Beer Release"; strings.Join(got, ",") != want {
		t.Fatalf("rows = %v, want %s", got, want)
	}
	if topper.DateRange != "September 13-19" {
		t.Fatalf("date range = %q", topper.DateRange)
	}
	if topper.Site != "example.com" {
		t.Fatalf("site = %q, want the host from the public URL template", topper.Site)
	}
	if got := topper.Rows[1].Bullets; len(got) != 2 || got[0] != "New hazy IPA" {
		t.Fatalf("bullets = %q", got)
	}
	if topper.Heading != "This week at "+sf.tenant.Name {
		t.Fatalf("heading = %q", topper.Heading)
	}
}
