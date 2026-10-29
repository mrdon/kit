package events

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"
)

func signTitles(evs []SignEvent) string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.Title
	}
	return strings.Join(out, ",")
}

// A Monday: two evening events, a standing offer, an offsite pour, a draft
// and a weekly series that is not on today.
func TestComposeSignPicksTheDay(t *testing.T) {
	loc := denver(t)
	day := time.Date(2026, 10, 26, 0, 0, 0, 0, loc)
	at := func(h, m int) time.Time { return time.Date(2026, 10, 26, h, m, 0, 0, loc) }
	events := []Event{
		screenEvent("Commander Night", at(17, 30), func(e *Event) { e.Summary = "Free Magic: The Gathering for all levels." }),
		screenEvent("Give Back: SPAN", at(15, 0), func(e *Event) { e.Prominence = ProminenceFeatured; e.EndsAt = nil }),
		screenEvent("BOGO Pizza", at(16, 0), func(e *Event) { e.Prominence = ProminenceAmenity }),
		screenEvent("Beer Fest", at(12, 0), func(e *Event) { e.Venue = VenueOffsite }),
		screenEvent("Draft Party", at(19, 0), func(e *Event) { e.Status = StatusDraft }),
		screenEvent("Bike Night", time.Date(2026, 1, 2, 18, 0, 0, 0, loc), func(e *Event) { e.RRule = "FREQ=WEEKLY;BYDAY=FR" }),
	}

	s := composeSign(events, day, "Gravity Brewing")

	if got, want := signTitles(s.Events), "Give Back: SPAN,Commander Night"; got != want {
		t.Errorf("events = %s, want %s", got, want)
	}
	if got := strings.Join(s.Also, "|"); got != "BOGO Pizza · 4 to 6pm" {
		t.Errorf("also = %q", got)
	}
	if s.Heading != "Today at Gravity Brewing" || s.Date != "Monday, October 26" {
		t.Errorf("head = %q / %q", s.Heading, s.Date)
	}
	if s.Events[0].Time != "3pm" {
		t.Errorf("no end should print just the start, got %q", s.Events[0].Time)
	}
	if s.Events[1].Time != "5:30 to 7:30pm" || len(s.Events[1].Lines) == 0 {
		t.Errorf("commander = %+v", s.Events[1])
	}
}

func TestComposeSignSaysTonightAndOverflows(t *testing.T) {
	loc := denver(t)
	day := time.Date(2026, 10, 30, 0, 0, 0, 0, loc)
	var events []Event
	for i, title := range []string{"One", "Two", "Three", "Four", "Five"} {
		events = append(events, screenEvent(title, time.Date(2026, 10, 30, 17+i, 0, 0, 0, loc)))
	}
	s := composeSign(events, day, "Gravity Brewing")
	if s.Heading != "Tonight at Gravity Brewing" {
		t.Errorf("heading = %q", s.Heading)
	}
	if len(s.Events) != signMaxEvents || strings.Join(s.Also, "|") != "Five · 9 to 11pm" {
		t.Errorf("events = %s, also = %v", signTitles(s.Events), s.Also)
	}
}

func TestSignDay(t *testing.T) {
	now := time.Date(2026, 10, 27, 21, 0, 0, 0, denver(t))
	if d, ok := signDay("", now); !ok || d.Day() != 27 || d.Hour() != 0 {
		t.Errorf("today = %v", d)
	}
	if d, ok := signDay("tomorrow", now); !ok || d.Day() != 28 {
		t.Errorf("tomorrow = %v", d)
	}
	if _, ok := signDay("friday", now); ok {
		t.Error("friday should be refused")
	}
}

func TestRenderSignPDF(t *testing.T) {
	poster := testPoster(t, 600, 900)
	for n := range 5 {
		s := Sign{Heading: "Tonight at Gravity Brewing", Site: "thegravitybrewing.com",
			Also: []string{"Happy Hour · 3 to 5pm", "Half price calzones"}}
		for range n {
			s.Events = append(s.Events, SignEvent{Title: "Cribbage Night", Time: "6 to 9pm", Poster: poster,
				Lines: []string{"Free to play, drop in any time. We have cards and boards to borrow."}})
		}
		var buf bytes.Buffer
		if err := RenderSignPDF(s, &buf); err != nil {
			t.Fatalf("%d events: %v", n, err)
		}
		if out := os.Getenv("KIT_SIGN_OUT"); out != "" {
			if err := os.WriteFile(strings.Replace(out, ".pdf", "-"+string(rune('0'+n))+".pdf", 1), buf.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	var buf bytes.Buffer
	if err := RenderSignPosterPDF(poster, &buf); err != nil || buf.Len() == 0 {
		t.Fatalf("poster page: %v", err)
	}
}

// testPoster is a plain portrait image, standing in for an uploaded poster.
func testPoster(t *testing.T, w, h int) *signImage {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x % 256), 90, uint8(y % 256), 255})
		}
	}
	var raw bytes.Buffer
	if err := png.Encode(&raw, img); err != nil {
		t.Fatal(err)
	}
	s, err := signFit(raw.Bytes(), signThumbPx)
	if err != nil {
		t.Fatal(err)
	}
	if s.W != 600 || s.H != 900 {
		t.Errorf("fit changed a small poster's size: %dx%d", s.W, s.H)
	}
	return s
}
