package events

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// A printed card is handed to customers, so it obeys the same predicate the
// feed does. This is the leak test for the print path.
func TestTopperRowsOnlyPublicEvents(t *testing.T) {
	loc := denver(t)
	start := time.Date(2026, 8, 2, 0, 0, 0, 0, loc)
	at := start.AddDate(0, 0, 3).Add(19 * time.Hour)

	events := []Event{
		{Title: "Trivia", StartsAt: at, Timezone: "America/Denver",
			Status: StatusPublished, Visibility: VisibilityPublic},
		{Title: "Sarah's 40th", StartsAt: at, Timezone: "America/Denver",
			Status: StatusPublished, Visibility: VisibilityPrivate},
		{Title: "Draft Night", StartsAt: at, Timezone: "America/Denver",
			Status: StatusDraft, Visibility: VisibilityPublic},
		{Title: "Called Off", StartsAt: at, Timezone: "America/Denver",
			Status: StatusCancelled, Visibility: VisibilityPublic},
	}

	rows := topperRows(events, start, start.AddDate(0, 0, 7), loc)
	if len(rows) != 1 || rows[0].Title != "Trivia" {
		t.Fatalf("rows = %+v, want only Trivia", rows)
	}
}

// A weekly series stores the week it began. The card has to say the date it
// happens THIS week, which is the whole reason rows are occurrences rather
// than events.
func TestTopperRowsExpandsRecurringSeries(t *testing.T) {
	loc := denver(t)
	events := []Event{{
		Title:      "Trivia",
		StartsAt:   time.Date(2024, 1, 3, 18, 30, 0, 0, loc),
		Timezone:   "America/Denver",
		RRule:      "FREQ=WEEKLY;BYDAY=WE",
		Status:     StatusPublished,
		Visibility: VisibilityPublic,
	}}
	start := time.Date(2026, 8, 2, 0, 0, 0, 0, loc)

	rows := topperRows(events, start, start.AddDate(0, 0, 7), loc)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].Day != "WED" || rows[0].Time != "6:30pm" {
		t.Fatalf("row = %q %q, want WED 6:30pm", rows[0].Day, rows[0].Time)
	}
}

// An off week for a fortnightly event must produce no band at all, rather than
// a band on the wrong date.
func TestTopperRowsSkipsWeekWithNoOccurrence(t *testing.T) {
	loc := denver(t)
	events := []Event{{
		Title:      "Market",
		StartsAt:   time.Date(2026, 8, 5, 17, 0, 0, 0, loc),
		Timezone:   "America/Denver",
		RRule:      "FREQ=WEEKLY;INTERVAL=2;BYDAY=WE",
		Status:     StatusPublished,
		Visibility: VisibilityPublic,
	}}
	start := time.Date(2026, 8, 9, 0, 0, 0, 0, loc)

	if rows := topperRows(events, start, start.AddDate(0, 0, 7), loc); len(rows) != 0 {
		t.Fatalf("rows = %+v, want none in the off week", rows)
	}
}

func TestTopperRowsSortedByTime(t *testing.T) {
	loc := denver(t)
	start := time.Date(2026, 8, 2, 0, 0, 0, 0, loc)
	mk := func(title string, day, hour int) Event {
		return Event{
			Title:      title,
			StartsAt:   start.AddDate(0, 0, day).Add(time.Duration(hour) * time.Hour),
			Timezone:   "America/Denver",
			Status:     StatusPublished,
			Visibility: VisibilityPublic,
		}
	}
	events := []Event{mk("Saturday", 6, 14), mk("Monday", 1, 17), mk("Thursday", 4, 18)}

	rows := topperRows(events, start, start.AddDate(0, 0, 7), loc)
	var got []string
	for _, r := range rows {
		got = append(got, r.Day)
	}
	if strings.Join(got, ",") != "MON,THU,SAT" {
		t.Fatalf("days = %v, want MON,THU,SAT", got)
	}
}

// An all-day event has no door time to print, and inventing midnight would be
// worse than printing nothing.
func TestTopperRowsAllDayHasNoTime(t *testing.T) {
	loc := denver(t)
	start := time.Date(2026, 8, 2, 0, 0, 0, 0, loc)
	events := []Event{{
		Title:      "Street Faire",
		StartsAt:   start.AddDate(0, 0, 6),
		AllDay:     true,
		Timezone:   "America/Denver",
		Status:     StatusPublished,
		Visibility: VisibilityPublic,
	}}

	rows := topperRows(events, start, start.AddDate(0, 0, 7), loc)
	if len(rows) != 1 || rows[0].Time != "" {
		t.Fatalf("row = %+v, want no time", rows)
	}
}

func TestTopperBullets(t *testing.T) {
	tests := []struct {
		name string
		e    Event
		want []string
	}{
		{
			name: "multi-line description becomes bullets",
			e:    Event{Description: "Buy one get one\nMembers only\nDine in only\nAnd a fourth"},
			want: []string{"Buy one get one", "Members only"},
		},
		{
			name: "markdown dashes are stripped",
			e:    Event{Description: "- First thing\n* Second thing"},
			want: []string{"First thing", "Second thing"},
		},
		{
			name: "summary splits at sentences",
			e:    Event{Summary: "Quiz night every Wednesday. Free to play."},
			want: []string{"Quiz night every Wednesday", "Free to play"},
		},
		{
			name: "one-line description falls back to summary",
			e:    Event{Summary: "Short and sweet.", Description: "One line only"},
			want: []string{"Short and sweet"},
		},
		{
			name: "nothing to say",
			e:    Event{},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := topperBullets(&tt.e)
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Fatalf("bullets = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTruncateWordsClipsAtWordBoundary(t *testing.T) {
	got := truncateWords("the quick brown fox jumps over the lazy dog", 20)
	if got != "the quick brown fox…" {
		t.Fatalf("got %q", got)
	}
	if got := truncateWords("short", 20); got != "short" {
		t.Fatalf("unchanged string got %q", got)
	}
}

// The week starts on Sunday because that is when the card goes out, and it
// snaps in the venue's own zone -- an event at 7pm Saturday must not roll into
// next week because UTC has already ticked over.
func TestWeekStartSnapsToSunday(t *testing.T) {
	loc := denver(t)
	// Aug 2 2026 is a Sunday; every day up to Saturday the 8th belongs to it.
	for _, day := range []int{2, 3, 5, 8} {
		got := weekStart(time.Date(2026, 8, day, 22, 30, 0, 0, loc))
		if got.Day() != 2 || got.Hour() != 0 {
			t.Fatalf("weekStart(Aug %d) = %s, want Aug 2 00:00", day, got)
		}
	}
	if got := weekStart(time.Date(2026, 8, 9, 1, 0, 0, 0, loc)); got.Day() != 9 {
		t.Fatalf("Sunday should be its own week start, got %s", got)
	}
}

func TestTopperRange(t *testing.T) {
	loc := denver(t)
	within := topperRange(time.Date(2026, 8, 2, 0, 0, 0, 0, loc), loc)
	if within != "August 2-8" {
		t.Fatalf("got %q", within)
	}
	across := topperRange(time.Date(2026, 8, 30, 0, 0, 0, 0, loc), loc)
	if !strings.HasPrefix(across, "Aug 30 ") || !strings.HasSuffix(across, "Sep 5") {
		t.Fatalf("got %q", across)
	}
}

func TestSiteHost(t *testing.T) {
	tests := map[string]string{
		"https://www.gravitybrewing.com/events/{slug}": "gravitybrewing.com",
		"https://gravitybrewing.com/e/{slug}":          "gravitybrewing.com",
		"":                                             "",
		"not a url":                                    "",
	}
	for in, want := range tests {
		if got := siteHost(in); got != want {
			t.Fatalf("siteHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTopperWeekParam(t *testing.T) {
	prev := timeNow
	timeNow = func() time.Time { return time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { timeNow = prev })

	this, err := topperWeekParam("")
	if err != nil || this.Day() != 5 {
		t.Fatalf("blank = %s, %v", this, err)
	}
	next, err := topperWeekParam("next")
	if err != nil || next.Day() != 12 {
		t.Fatalf("next = %s, %v", next, err)
	}
	explicit, err := topperWeekParam("2026-12-25")
	if err != nil || explicit.Month() != time.December {
		t.Fatalf("explicit = %s, %v", explicit, err)
	}
	if _, err := topperWeekParam("last tuesday"); err == nil {
		t.Fatal("want an error for unparseable week")
	}
}

// The renderer has to survive the awkward weeks, not just the tidy one: no
// events at all, and more events than bands.
func TestRenderTopperPDF(t *testing.T) {
	base := sampleTopper()
	cases := map[string]Topper{
		"typical": base,
		"empty":   {Heading: "This week at Gravity Brewing", DateRange: "August 2-8"},
		"full":    {Heading: base.Heading, DateRange: base.DateRange, Rows: append(append([]TopperRow{}, base.Rows...), base.Rows[:2]...)},
	}
	for name, topper := range cases {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := RenderTopperPDF(topper, &buf); err != nil {
				t.Fatalf("RenderTopperPDF: %v", err)
			}
			if !bytes.HasPrefix(buf.Bytes(), []byte("%PDF-")) {
				t.Fatal("output is not a PDF")
			}
			if buf.Len() < 2000 {
				t.Fatalf("suspiciously small PDF: %d bytes", buf.Len())
			}
		})
	}
}

// A day with two events is one band, not two stacked bands wearing the same
// day label. This is the case the real calendar already has: Bike Night and
// the Re-Launch Party both fall on Saturday.
func TestTopperGroupsEventsByDay(t *testing.T) {
	loc := denver(t)
	start := time.Date(2026, 8, 2, 0, 0, 0, 0, loc)
	mk := func(title string, day, hour int, p Prominence) Event {
		return Event{
			Title:      title,
			StartsAt:   start.AddDate(0, 0, day).Add(time.Duration(hour) * time.Hour),
			Timezone:   "America/Denver",
			Status:     StatusPublished,
			Visibility: VisibilityPublic,
			Prominence: p,
		}
	}
	events := []Event{
		mk("Bike Night", 6, 18, ProminenceNormal),
		mk("Re-Launch Party", 6, 14, ProminenceNormal),
	}

	rows := topperRows(events, start, start.AddDate(0, 0, 7), loc)
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want one band for the day", len(rows))
	}
	// Equal prominence, so the earlier door time headlines.
	if rows[0].Title != "Re-Launch Party" {
		t.Fatalf("headliner = %q, want the earlier event", rows[0].Title)
	}
	if got := rows[0].Bullets; len(got) != 1 || got[0] != "Also: Bike Night · 6pm" {
		t.Fatalf("bullets = %q, want the other event named with its time", got)
	}
}

// The whole reason prominence exists: a standing pizza offer must not take the
// headline off a real event, whatever time either starts.
func TestTopperBackgroundNeverHeadlinesOverARealEvent(t *testing.T) {
	loc := denver(t)
	start := time.Date(2026, 8, 2, 0, 0, 0, 0, loc)
	mk := func(title string, hour int, p Prominence) Event {
		return Event{
			Title:      title,
			StartsAt:   start.AddDate(0, 0, 1).Add(time.Duration(hour) * time.Hour),
			Timezone:   "America/Denver",
			Status:     StatusPublished,
			Visibility: VisibilityPublic,
			Prominence: p,
		}
	}
	// The offer starts earlier, so time alone would put it on top.
	events := []Event{
		mk("Half-price pizza", 16, ProminenceBackground),
		mk("Bike Night", 18, ProminenceNormal),
	}

	rows := topperRows(events, start, start.AddDate(0, 0, 7), loc)
	if len(rows) != 1 || rows[0].Title != "Bike Night" {
		t.Fatalf("headliner = %+v, want Bike Night", rows)
	}
	if got := rows[0].Bullets; len(got) != 1 || got[0] != "Also: Half-price pizza · 4pm" {
		t.Fatalf("bullets = %q, want the offer demoted to a bullet", got)
	}
}

// Featured outranks a normal event even when the normal one starts first.
func TestTopperFeaturedHeadlinesItsDay(t *testing.T) {
	loc := denver(t)
	start := time.Date(2026, 8, 2, 0, 0, 0, 0, loc)
	mk := func(title string, hour int, p Prominence) Event {
		return Event{
			Title:      title,
			StartsAt:   start.AddDate(0, 0, 5).Add(time.Duration(hour) * time.Hour),
			Timezone:   "America/Denver",
			Status:     StatusPublished,
			Visibility: VisibilityPublic,
			Prominence: p,
		}
	}
	events := []Event{
		mk("Bike Night", 15, ProminenceNormal),
		mk("Anniversary Party", 19, ProminenceFeatured),
	}

	rows := topperRows(events, start, start.AddDate(0, 0, 7), loc)
	if len(rows) != 1 || rows[0].Title != "Anniversary Party" {
		t.Fatalf("headliner = %+v, want the featured event", rows)
	}
}

// On a day with nothing else, a standing offer headlines by default rather
// than by promotion -- it is what is on. This is the case the reference card
// prints as MON / BOGO PIZZA.
func TestTopperBackgroundHeadlinesAQuietDay(t *testing.T) {
	loc := denver(t)
	start := time.Date(2026, 8, 2, 0, 0, 0, 0, loc)
	events := []Event{{
		Title:      "Half-price pizza",
		StartsAt:   start.AddDate(0, 0, 1).Add(16 * time.Hour),
		Timezone:   "America/Denver",
		Status:     StatusPublished,
		Visibility: VisibilityPublic,
		Prominence: ProminenceBackground,
	}}

	rows := topperRows(events, start, start.AddDate(0, 0, 7), loc)
	if len(rows) != 1 || rows[0].Title != "Half-price pizza" {
		t.Fatalf("rows = %+v, want the offer headlining an otherwise empty day", rows)
	}
}

func TestBandBullets(t *testing.T) {
	own := []string{"first", "second", "third"}

	// No support acts: the headliner keeps its own list.
	if got := bandBullets(own, nil); strings.Join(got, "|") != "first|second|third" {
		t.Fatalf("alone = %q", got)
	}
	// Two support acts squeeze the headliner's detail, never below one line,
	// and reading order still puts the headliner's own bullets first.
	got := bandBullets(own, []string{"Bike Night · 6pm", "Cask tapping · 7pm"})
	if strings.Join(got, "|") != "first|Also: Bike Night · 6pm|Cask tapping · 7pm" {
		t.Fatalf("with supports = %q", got)
	}
	// A very busy day names what fits and counts the rest rather than
	// pretending the others are not happening.
	got = bandBullets(own, []string{"A · 1pm", "B · 2pm", "C · 3pm", "D · 4pm", "E · 5pm"})
	if strings.Join(got, "|") != "first|Also: A · 1pm|B · 2pm|+3 more" {
		t.Fatalf("busy day = %q", got)
	}
}

func TestBillingRankTreatsUnknownValuesAsNormal(t *testing.T) {
	if billingRank(Prominence("wat")) != billingRank(ProminenceNormal) {
		t.Fatal("an unknown prominence should behave like a normal event")
	}
	if billingRank(ProminenceFeatured) >= billingRank(ProminenceNormal) {
		t.Fatal("featured must outrank normal")
	}
	if billingRank(ProminenceBackground) <= billingRank(ProminenceNormal) {
		t.Fatal("background must rank below normal")
	}
}

// The Double D's case: one standing offer that runs Sunday, Monday and
// Tuesday. It is a single weekly event on three weekdays -- not a three-day
// event, and not three events -- so it must produce three bands in one week,
// each one a background item that would yield to a real event on its day.
func TestTopperMultiWeekdayStandingOffer(t *testing.T) {
	loc := denver(t)
	start := time.Date(2026, 8, 2, 0, 0, 0, 0, loc) // a Sunday
	events := []Event{{
		Title:      "Pizza Together",
		Summary:    "Buy any sourdough pizza and get one free when you dine in.",
		StartsAt:   start.Add(16 * time.Hour),
		Timezone:   "America/Denver",
		RRule:      "FREQ=WEEKLY;BYDAY=SU,MO,TU",
		Status:     StatusPublished,
		Visibility: VisibilityPublic,
		Prominence: ProminenceBackground,
	}}

	rows := topperRows(events, start, start.AddDate(0, 0, 7), loc)
	var days []string
	for _, r := range rows {
		days = append(days, r.Day)
	}
	if strings.Join(days, ",") != "SUN,MON,TUE" {
		t.Fatalf("days = %v, want SUN,MON,TUE", days)
	}
	// It headlines each of those days only because nothing else is on them.
	for _, r := range rows {
		if r.Title != "Pizza Together" {
			t.Fatalf("row %s = %q", r.Day, r.Title)
		}
	}

	// Put a real event on the Monday and the offer must step aside.
	events = append(events, Event{
		Title:      "Bike Night",
		StartsAt:   start.AddDate(0, 0, 1).Add(18 * time.Hour),
		Timezone:   "America/Denver",
		Status:     StatusPublished,
		Visibility: VisibilityPublic,
		Prominence: ProminenceNormal,
	})
	rows = topperRows(events, start, start.AddDate(0, 0, 7), loc)
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want three days still", len(rows))
	}
	mon := rows[1]
	if mon.Day != "MON" || mon.Title != "Bike Night" {
		t.Fatalf("monday = %s %q, want the real event headlining", mon.Day, mon.Title)
	}
	if len(mon.Bullets) == 0 || mon.Bullets[len(mon.Bullets)-1] != "Also: Pizza Together · 4pm" {
		t.Fatalf("monday bullets = %q, want the offer demoted", mon.Bullets)
	}
}
