package events

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func withPoster(e *Event) {
	id := uuid.New()
	e.HeroAttachmentID = &id
}

func boardTitles(evs []BoardEvent) string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.Title
	}
	return strings.Join(out, ",")
}

// The week the board was built for: Halloween on the Saturday, a one-off
// cribbage night, a Give Back Monday, a Battles & Brews series on set dates,
// and the weekly regulars, frozen on a Monday afternoon.
func TestComposeBoardFeedPicksTheWallsEvents(t *testing.T) {
	now := screenNow(t) // Wed Aug 5, noon
	loc := now.Location()
	at := func(m time.Month, d, h int) time.Time { return time.Date(2026, m, d, h, 0, 0, 0, loc) }

	cribbage := screenEvent("Cribbage Night", at(8, 6, 18), withPoster, func(e *Event) {
		e.Prominence = ProminenceFeatured
		e.Summary = "Beginners welcome."
	})
	events := []Event{
		screenEvent("Halloween Party", at(8, 8, 12), withPoster),
		cribbage,
		screenEvent("Give Back: SPAN", at(8, 10, 15), withPoster, func(e *Event) { e.Labels = []string{"giveback"} }),
		screenEvent("Battles & Brews", at(7, 28, 17), withPoster, func(e *Event) {
			e.RDates = []time.Time{at(8, 11, 17), at(9, 8, 17)}
		}),
		screenEvent("No Poster Night", at(8, 7, 18)),
		screenEvent("Over Already", at(8, 5, 8), withPoster),
		screenEvent("Featured Fest", at(8, 9, 12), func(e *Event) { e.Prominence = ProminenceFeatured }),
		screenEvent("Next Month", at(8, 20, 18), withPoster),
		screenEvent("Beer Fest", at(8, 9, 12), withPoster, func(e *Event) { e.Venue = VenueOffsite }),
		screenEvent("Draft Party", at(8, 9, 12), withPoster, func(e *Event) { e.Status = StatusDraft }),
		screenEvent("Trivia", at(1, 7, 18), func(e *Event) { e.RRule = "FREQ=WEEKLY;BYDAY=WE" }),
	}

	f := composeBoardFeed(events, now, BoardRequest{Labels: []string{"giveback"}, Featured: true})

	// Cribbage is the featured panel's, SPAN the Give Back one's,
	// the rest are unposted, over, too far out, offsite, unpublished or weekly.
	if got, want := boardTitles(f.Upcoming), "Halloween Party,Battles & Brews"; got != want {
		t.Errorf("upcoming = %s, want %s", got, want)
	}
	if f.Upcoming[0].When != "Saturday" {
		t.Errorf("halloween when = %q", f.Upcoming[0].When)
	}
	if got := f.ByLabel["giveback"].Title; got != "Give Back: SPAN" {
		t.Errorf("giveback = %q", got)
	}
	// Featured Fest is featured too but has no poster to show.
	if f.Featured == nil || f.Featured.EventID != cribbage.ID {
		t.Fatalf("featured = %+v, want cribbage", f.Featured)
	}
	if f.Featured.When != "Tomorrow" || f.Featured.Time != "6 to 8pm" || f.Featured.Summary != "Beginners welcome." {
		t.Errorf("featured = %+v", *f.Featured)
	}

	// The morning after, cribbage is over: nothing featured is left.
	if after := composeBoardFeed(events, at(8, 7, 9), BoardRequest{Featured: true}); after.Featured != nil {
		t.Errorf("featured after cribbage = %+v", *after.Featured)
	}

	// Without a featured panel to claim it, cribbage gets a poster like any night.
	if got := boardTitles(composeBoardFeed(events, now, BoardRequest{}).Upcoming); !strings.HasPrefix(got, "Cribbage Night,") {
		t.Errorf("upcoming without featured = %s", got)
	}
}

func TestBoardWeeklyReadsAsAWeek(t *testing.T) {
	now := screenNow(t)
	loc := now.Location()
	series := func(title, rule string, h, m int, opts ...func(*Event)) Event {
		start := time.Date(2026, 1, 4, h, m, 0, 0, loc)
		e := screenEvent(title, start, append(opts, func(e *Event) { e.RRule = rule })...)
		end := start.Add(3 * time.Hour)
		e.EndsAt = &end
		return e
	}
	events := []Event{
		series("Bike Night", "FREQ=WEEKLY;BYDAY=FR", 18, 0),
		series("Guesstronauts Trivia Game Night", "FREQ=WEEKLY;BYDAY=TH", 18, 30, func(e *Event) {
			e.Summary = "Trivia crossed with betting. The first game show we brewed ourselves."
		}),
		series("Vinyl Night", "FREQ=WEEKLY;BYDAY=MO", 17, 0),
		series("BOGO Pizza", "FREQ=WEEKLY;BYDAY=MO,TU", 16, 0, func(e *Event) { e.Prominence = ProminenceAmenity }),
		series("Happy Hour", "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR", 15, 0, func(e *Event) {
			e.Prominence = ProminenceBackground
		}),
		series("Ended", "FREQ=WEEKLY;BYDAY=SA;UNTIL=20260301T000000Z", 12, 0),
	}

	got := boardWeekly(events, now)
	var lines []string
	for _, w := range got {
		lines = append(lines, w.When+" "+w.Time+" "+w.Title)
	}
	want := []string{
		"Every Mon 5 to 8pm Vinyl Night",
		"Every Thu 6:30 to 9:30pm Guesstronauts Trivia Game Night",
		"Every Fri 6 to 9pm Bike Night",
	}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Errorf("weekly =\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if got[1].Note != "Trivia crossed with betting" {
		t.Errorf("note = %q", got[1].Note)
	}
}

func TestEveryDays(t *testing.T) {
	for _, tc := range []struct {
		days []time.Weekday
		want string
	}{
		{[]time.Weekday{time.Monday}, "Every Mon"},
		{[]time.Weekday{time.Monday, time.Tuesday}, "Every Mon & Tue"},
		{[]time.Weekday{time.Monday, time.Wednesday, time.Friday}, "Every Mon, Wed & Fri"},
	} {
		if got := everyDays(tc.days); got != tc.want {
			t.Errorf("everyDays(%v) = %q, want %q", tc.days, got, tc.want)
		}
	}
}

func TestTimeRange(t *testing.T) {
	loc := time.UTC
	at := func(h, m int) time.Time { return time.Date(2026, 1, 1, h, m, 0, 0, loc) }
	for _, tc := range []struct {
		from, to time.Time
		want     string
	}{
		{at(17, 0), at(21, 0), "5 to 9pm"},
		{at(11, 0), at(14, 0), "11am to 2pm"},
		{at(17, 30), at(20, 30), "5:30 to 8:30pm"},
		{at(18, 30), at(18, 30), "6:30pm"},
	} {
		if got := timeRange(tc.from, tc.to); got != tc.want {
			t.Errorf("timeRange = %q, want %q", got, tc.want)
		}
	}
}
