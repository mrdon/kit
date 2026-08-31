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
			want: []string{"Buy one get one", "Members only", "Dine in only"},
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
		"full":    {Heading: base.Heading, DateRange: base.DateRange, Rows: append(append([]TopperRow{}, base.Rows...), base.Rows...)[:topperMaxRows], More: 3},
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
