package events

import (
	"bytes"
	"html/template"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// screenEvent is a published, public event in Denver, two hours long.
func screenEvent(title string, start time.Time, opts ...func(*Event)) Event {
	end := start.Add(2 * time.Hour)
	e := Event{
		ID:         uuid.New(),
		Title:      title,
		Timezone:   "America/Denver",
		StartsAt:   start,
		EndsAt:     &end,
		Status:     StatusPublished,
		Visibility: VisibilityPublic,
		Venue:      VenueOnsite,
		Prominence: ProminenceNormal,
	}
	for _, o := range opts {
		o(&e)
	}
	return e
}

func titles(cards []screenCard) string {
	out := make([]string, len(cards))
	for i, c := range cards {
		out[i] = c.Title
	}
	return strings.Join(out, ",")
}

// A frozen Wednesday noon, passed in rather than read from the clock: these
// tests are about where events land relative to a specific day.
func screenNow(t *testing.T) time.Time {
	return time.Date(2026, 8, 5, 12, 0, 0, 0, denver(t))
}

func TestComposeScreenSortsEventsOntoTheRightSlides(t *testing.T) {
	now := screenNow(t)
	loc := now.Location()
	at := func(m time.Month, d, h int) time.Time { return time.Date(2026, m, d, h, 0, 0, 0, loc) }

	events := []Event{
		screenEvent("Trivia", time.Date(2026, 1, 7, 18, 30, 0, 0, loc), func(e *Event) {
			e.RRule = "FREQ=WEEKLY;BYDAY=WE"
		}),
		screenEvent("Happy Hour", time.Date(2026, 1, 1, 15, 0, 0, 0, loc), func(e *Event) {
			e.RRule = "FREQ=WEEKLY;BYDAY=SU,MO,TU,WE,TH,FR,SA"
			e.Prominence = ProminenceBackground
		}),
		screenEvent("Bike Night", at(8, 7, 18)),
		screenEvent("Anniversary Party", at(8, 22, 14), func(e *Event) { e.Prominence = ProminenceFeatured }),
		screenEvent("Harvest Fest", at(9, 12, 12), func(e *Event) { e.Venue = VenueOffsite; e.Location = "Main St" }),
		screenEvent("Morning Yoga", at(8, 5, 8)),
		screenEvent("Sarah's 40th", at(8, 6, 18), func(e *Event) { e.Visibility = VisibilityPrivate }),
		screenEvent("Next Year", at(12, 20, 18), func(e *Event) { e.Prominence = ProminenceFeatured }),
	}

	s := composeScreen(events, now, "Gravity", "example.com")

	// One card per event, soonest first; yoga is over, happy hour is a
	// standing offer, the booking is private and December is past the horizon.
	if got, want := titles(s.Overview), "Trivia,Bike Night,Anniversary Party,Harvest Fest"; got != want {
		t.Errorf("overview = %s, want %s", got, want)
	}
	if got := titles(s.Featured); got != "Anniversary Party" {
		t.Errorf("featured = %s", got)
	}
	// Coming soon starts after this week, so Bike Night and the weekly trivia
	// are the week slide's, not repeated here.
	if got := titles(s.Soon); got != "Anniversary Party,Harvest Fest" {
		t.Errorf("soon = %s", got)
	}
	if len(s.Week) != 7 {
		t.Fatalf("week rows = %d, want one per day (happy hour is daily)", len(s.Week))
	}
	if s.Week[0].Label != "Today" || s.Week[0].Title != "Trivia" {
		t.Errorf("first week row = %+v, want today headlined by trivia", s.Week[0])
	}
	if s.Week[1].Label != "Tomorrow" || s.Week[1].Title != "Happy Hour" {
		t.Errorf("second week row = %+v", s.Week[1])
	}
	for _, d := range s.Week {
		if d.Title == "Sarah's 40th" {
			t.Fatal("a private booking reached the wall")
		}
	}
	if got := s.Overview[0].Relative; got != "Tonight" {
		t.Errorf("trivia at 6:30 today should read Tonight, got %q", got)
	}
	if got := s.Featured[0].Relative; got != "In 17 days" {
		t.Errorf("party relative = %q", got)
	}
}

func TestRelativeDay(t *testing.T) {
	now := screenNow(t)
	loc := now.Location()
	cases := []struct {
		at     time.Time
		allDay bool
		want   string
	}{
		{time.Date(2026, 8, 5, 13, 0, 0, 0, loc), false, "Today"},
		{time.Date(2026, 8, 5, 19, 0, 0, 0, loc), false, "Tonight"},
		{time.Date(2026, 8, 5, 0, 0, 0, 0, loc), true, "Today"},
		{time.Date(2026, 8, 6, 19, 0, 0, 0, loc), false, "Tomorrow"},
		{time.Date(2026, 8, 8, 19, 0, 0, 0, loc), false, "Saturday"},
		{time.Date(2026, 8, 12, 19, 0, 0, 0, loc), false, "In 7 days"},
	}
	for _, c := range cases {
		if got := relativeDay(c.at, now, c.allDay); got != c.want {
			t.Errorf("relativeDay(%s) = %q, want %q", c.at, got, c.want)
		}
	}
}

// The stamp is what reloads a wall screen, so it has to move when the
// programme or the date moves and hold still otherwise.
func TestScreenVersionMovesWithContentAndDate(t *testing.T) {
	now := screenNow(t)
	events := []Event{screenEvent("Bike Night", now.Add(48*time.Hour))}

	a := screenVersion(composeScreen(events, now, "Gravity", ""), nil)
	if b := screenVersion(composeScreen(events, now.Add(time.Hour), "Gravity", ""), nil); a != b {
		t.Error("stamp moved within the same day with nothing changed")
	}
	if b := screenVersion(composeScreen(events, now.AddDate(0, 0, 1), "Gravity", ""), nil); a == b {
		t.Error("stamp did not move at midnight")
	}
	events[0].Title = "Bike Night & BBQ"
	if b := screenVersion(composeScreen(events, now, "Gravity", ""), nil); a == b {
		t.Error("stamp did not move when a title changed")
	}
}

func TestRenderScreen(t *testing.T) {
	now := screenNow(t)
	loc := now.Location()
	poster := uuid.New()
	events := []Event{
		screenEvent("Bike Night <b>", time.Date(2026, 8, 7, 18, 0, 0, 0, loc)),
		screenEvent("Anniversary", time.Date(2026, 8, 22, 14, 0, 0, 0, loc), func(e *Event) {
			e.Prominence = ProminenceFeatured
			e.HeroAttachmentID = &poster
		}),
	}
	s := composeScreen(events, now, "Gravity", "example.com")
	posters := map[string]template.URL{poster.String(): "data:image/jpeg;base64,AAAA"}

	page, err := renderScreen(s, "v1", posters, nil)
	if err != nil {
		t.Fatalf("renderScreen: %v", err)
	}
	html := string(page)
	for _, want := range []string{
		`data-version="v1"`,
		`Bike Night &lt;b&gt;`,
		`src="data:image/jpeg;base64,AAAA"`,
		`class="s s-overview"`, `class="s s-week"`, `class="s s-soon"`,
		`data-auto-animate`,
		`style="--i: 2"`,
		`Reveal.initialize`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("page is missing %q", want)
		}
	}
	// html/template's marker for a value it refused -- a data URI or style it
	// did not trust renders as this, and on a wall that is a silent blank.
	if strings.Contains(html, "ZgotmplZ") {
		t.Error("page contains a value html/template refused")
	}
	if strings.Contains(html, "s-empty") {
		t.Error("empty slide rendered alongside real ones")
	}
}

func TestRenderScreenWithNothingOn(t *testing.T) {
	s := composeScreen(nil, screenNow(t), "Gravity", "example.com")
	page, err := renderScreen(s, "v1", nil, nil)
	if err != nil {
		t.Fatalf("renderScreen: %v", err)
	}
	if !strings.Contains(string(page), "See what's next at example.com") {
		t.Error("an empty programme should still say something on the wall")
	}
}

func TestScreenPosterFitsAndKeepsShape(t *testing.T) {
	raw := solidPNG(t, 2000, 3000)
	out, err := screenPoster(raw)
	if err != nil {
		t.Fatalf("screenPoster: %v", err)
	}
	w, h := decodedSize(t, out)
	if h != screenPosterPx || w != 640 {
		t.Errorf("fitted to %dx%d, want 640x%d", w, h, screenPosterPx)
	}
}

// Through the real route and the real query: the page answers without a
// session, and only public events reach it.
func TestScreenRouteServesPublicEventsOnly(t *testing.T) {
	sf := newSyncFixture(t)
	public := sf.create(t, CreateParams{Title: "Bike Night", StartsAt: defaultFixtureStart(), Visibility: VisibilityPublic})
	sf.publish(t, public)
	private := sf.create(t, CreateParams{Title: "Sarah's 40th", StartsAt: defaultFixtureStart(), Visibility: VisibilityPrivate})
	sf.publish(t, private)

	mux := http.NewServeMux()
	registerScreenRoutes(mux, sf.app)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/"+sf.tenant.Slug+"/events/screen", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Bike Night") {
		t.Error("public event missing from the screen")
	}
	if strings.Contains(body, "Sarah") {
		t.Error("a private booking reached the screen")
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q", cc)
	}

	ver := httptest.NewRecorder()
	mux.ServeHTTP(ver, httptest.NewRequest(http.MethodGet, "/"+sf.tenant.Slug+"/events/screen.version", nil))
	if !strings.Contains(body, `data-version="`+ver.Body.String()+`"`) {
		t.Errorf("version route %q does not match the page's stamp", ver.Body.String())
	}
}

func solidPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encoding png: %v", err)
	}
	return buf.Bytes()
}

func decodedSize(t *testing.T, raw []byte) (int, int) {
	t.Helper()
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decoding: %v", err)
	}
	return cfg.Width, cfg.Height
}
