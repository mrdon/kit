package events

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func (sf *syncFixture) feed(t *testing.T) Feed {
	t.Helper()
	f, err := sf.svc.BuildFeed(sf.ctx, sf.tenant.ID, "")
	if err != nil {
		t.Fatalf("BuildFeed: %v", err)
	}
	return f
}

func (f Feed) titles() []string {
	out := make([]string, len(f.Events))
	for i, e := range f.Events {
		out[i] = e.Title
	}
	return out
}

// The leak test. Only a published AND public event may reach the open web;
// every other combination must be absent.
func TestFeedExcludesEverythingButPublishedPublic(t *testing.T) {
	sf := newSyncFixture(t)

	live := sf.create(t, CreateParams{Title: "Trivia Night", Visibility: VisibilityPublic})
	sf.publish(t, live)

	party := sf.create(t, CreateParams{Title: "Sarah's 40th", Visibility: VisibilityPrivate})
	sf.publish(t, party) // published, but private

	sf.create(t, CreateParams{Title: "Unfinished Draft", Visibility: VisibilityPublic}) // draft

	cancelled := sf.create(t, CreateParams{Title: "Called Off", Visibility: VisibilityPublic})
	sf.publish(t, cancelled)
	if _, err := sf.svc.Cancel(sf.ctx, sf.tenant.ID, cancelled.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	got := sf.feed(t).titles()
	if len(got) != 1 || got[0] != "Trivia Night" {
		t.Fatalf("feed = %v, want only [Trivia Night]", got)
	}
}

// prep_notes is the internal staff brief. It belongs on the calendar, which
// staff and the food partner read, and nowhere near the website.
func TestFeedNeverIncludesInternalFields(t *testing.T) {
	sf := newSyncFixture(t)
	attendance := 40
	e := sf.create(t, CreateParams{
		Title:              "Trivia Night",
		Visibility:         VisibilityPublic,
		Description:        "Come play",
		PrepNotes:          "SECRET-STAFF-NOTE: prep 40 glasses, comp the host",
		SpaceImpact:        SpaceImpactPartial,
		ExpectedAttendance: &attendance,
	})
	sf.publish(t, e)

	raw, err := json.Marshal(sf.feed(t))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(raw)
	for _, forbidden := range []string{
		"SECRET-STAFF-NOTE", "prep_notes", "expected_attendance",
		"space_impact", "notify_food_partner", "created_by",
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("public feed contains %q:\n%s", forbidden, body)
		}
	}
	// ...while the genuinely public copy is present.
	if !strings.Contains(body, "Come play") {
		t.Error("the public description is missing from the feed")
	}
}

// A weekly series stores its FIRST occurrence, which for a long-running night
// is years past. A naive date filter would silently drop it from the website
// forever, and nothing would report an error.
func TestFeedIncludesRecurringEventWithPastStart(t *testing.T) {
	sf := newSyncFixture(t)
	e := sf.create(t, CreateParams{
		Title: "Long Running Trivia", StartsAt: "2024-01-02 19:00",
		Visibility: VisibilityPublic, RRule: "FREQ=WEEKLY;BYDAY=TU",
	})
	sf.publish(t, e)

	// A genuinely finished one-off from the same era must NOT come back.
	old := sf.create(t, CreateParams{
		Title: "Old One Off", StartsAt: "2024-01-03 19:00", Visibility: VisibilityPublic,
	})
	sf.publish(t, old)

	got := sf.feed(t).titles()
	if len(got) != 1 || got[0] != "Long Running Trivia" {
		t.Fatalf("feed = %v, want only the recurring series", got)
	}
	if rule := sf.feed(t).Events[0].Recurrence; rule != "FREQ=WEEKLY;BYDAY=TU" {
		t.Errorf("recurrence = %q, want it passed through unexpanded for the site to expand", rule)
	}
}

// The wire contract carries type from day one so release/news kinds can be
// added later without breaking a build that already depends on it.
func TestFeedTypeMapsVenue(t *testing.T) {
	sf := newSyncFixture(t)
	onsite := sf.create(t, CreateParams{Title: "Trivia", Visibility: VisibilityPublic})
	sf.publish(t, onsite)
	offsite := sf.create(t, CreateParams{
		Title: "Denver Beer Fest", Visibility: VisibilityPublic, Venue: VenueOffsite,
	})
	sf.publish(t, offsite)

	byTitle := map[string]string{}
	for _, item := range sf.feed(t).Events {
		byTitle[item.Title] = item.Type
	}
	if byTitle["Trivia"] != "event" {
		t.Errorf("onsite type = %q, want event", byTitle["Trivia"])
	}
	// An offsite appearance is still public — "come see us there" belongs on
	// the site — it just renders differently.
	if byTitle["Denver Beer Fest"] != "festival" {
		t.Errorf("offsite type = %q, want festival", byTitle["Denver Beer Fest"])
	}
}

func TestFeedDerivesCanonicalURL(t *testing.T) {
	sf := newSyncFixture(t)
	e := sf.create(t, CreateParams{Title: "Trivia Night", Visibility: VisibilityPublic})
	sf.publish(t, e)

	item := sf.feed(t).Events[0]
	want := "https://example.com/events/trivia-night"
	if item.CanonicalURL != want {
		t.Errorf("canonical_url = %q, want %q", item.CanonicalURL, want)
	}
}

// HTTP-level behaviour: the token must actually be required.
func TestFeedHTTPRequiresToken(t *testing.T) {
	sf := newSyncFixture(t)
	e := sf.create(t, CreateParams{Title: "Trivia Night", Visibility: VisibilityPublic})
	sf.publish(t, e)

	mux := http.NewServeMux()
	sf.app.registerFeedRoutes(muxAdapter{mux})
	path := "/" + sf.tenant.Slug + "/events/feed.json"

	cases := []struct {
		name   string
		header string
		want   int
	}{
		{"no header", "", http.StatusUnauthorized},
		{"wrong token", "Bearer not-the-token", http.StatusUnauthorized},
		{"wrong scheme", "Basic " + sf.sett.FeedToken, http.StatusUnauthorized},
		{"correct token", "Bearer " + sf.sett.FeedToken, http.StatusOK},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if c.header != "" {
			req.Header.Set("Authorization", c.header)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s: status = %d, want %d (body %q)", c.name, rec.Code, c.want, rec.Body.String())
		}
	}
}

func TestFeedHTTPServesPublishedPublicOnly(t *testing.T) {
	sf := newSyncFixture(t)
	live := sf.create(t, CreateParams{Title: "Trivia Night", Visibility: VisibilityPublic})
	sf.publish(t, live)
	party := sf.create(t, CreateParams{Title: "Sarah's 40th", Visibility: VisibilityPrivate})
	sf.publish(t, party)

	mux := http.NewServeMux()
	sf.app.registerFeedRoutes(muxAdapter{mux})

	req := httptest.NewRequest(http.MethodGet, "/"+sf.tenant.Slug+"/events/feed.json", nil)
	req.Header.Set("Authorization", "Bearer "+sf.sett.FeedToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Trivia Night") {
		t.Error("the public event is missing from the served feed")
	}
	if strings.Contains(body, "Sarah") {
		t.Fatalf("a private booking was served on the public feed:\n%s", body)
	}

	var feed Feed
	if err := json.Unmarshal([]byte(body), &feed); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if _, err := time.Parse(time.RFC3339, feed.GeneratedAt); err != nil {
		t.Errorf("generated_at is not RFC 3339: %q", feed.GeneratedAt)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content type = %q", ct)
	}
}

// Before a token exists the endpoint 404s, so a misconfigured build fails
// loudly instead of quietly publishing an empty events page.
func TestFeedHTTPNotFoundBeforeConfigured(t *testing.T) {
	f := newFixture(t)
	app := &App{pool: f.pool, svc: f.svc}

	mux := http.NewServeMux()
	app.registerFeedRoutes(muxAdapter{mux})

	req := httptest.NewRequest(http.MethodGet, "/"+f.tenant.Slug+"/events/feed.json", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when no feed token is configured", rec.Code)
	}
}

func TestFeedURL(t *testing.T) {
	if got := FeedURL("https://kit.example.com/", "gravity"); got != "https://kit.example.com/gravity/events/feed.json" {
		t.Errorf("FeedURL = %q", got)
	}
}

// muxAdapter satisfies apps.Mux over a plain ServeMux for tests.
type muxAdapter struct{ m *http.ServeMux }

func (a muxAdapter) Handle(pattern string, h http.Handler) { a.m.Handle(pattern, h) }
func (a muxAdapter) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	a.m.HandleFunc(pattern, h)
}

// Without this the website would publish a three-date supper club as a one-off
// on the first of them: a static build cannot expand a recurrence, so Kit
// hands over the dates already worked out.
func TestFeedExpandsAnExplicitDateList(t *testing.T) {
	sf := newSyncFixture(t)
	// Anchored ahead of today so the expansion window always contains them.
	base := time.Now().AddDate(0, 1, 0)
	// Date built separately from the time: "18:00" inside a layout string is
	// not a literal -- Go reads the 1 as the month field.
	d := func(add int) string { return base.AddDate(0, 0, add).Format("2006-01-02") + " 18:00" }

	e := sf.create(t, CreateParams{
		Title: "Supper Club", StartsAt: d(0), Visibility: VisibilityPublic,
		Timezone: "America/Denver", RepeatDates: []string{d(28), d(56)},
	})
	sf.publish(t, e)

	items := sf.feed(t).Events
	// One item, not three: the whole point of the date list.
	if len(items) != 1 {
		t.Fatalf("expected a single feed item for the series, got %d", len(items))
	}
	if len(items[0].Upcoming) != 3 {
		t.Fatalf("expected all 3 dates, got %v", items[0].Upcoming)
	}
	for i, got := range items[0].Upcoming {
		want := base.AddDate(0, 0, i*28).Format("2006-01-02")
		if !strings.HasPrefix(got, want) {
			t.Errorf("occurrence %d = %s, want %s", i, got, want)
		}
		if !strings.Contains(got, "T18:00") {
			t.Errorf("occurrence %d lost the venue's wall clock: %s", i, got)
		}
	}
}

// The case the site could never have handled itself: "the first Friday" lands
// on a different date every month, and a monthly series' stored start may be
// long past.
func TestFeedExpandsAMonthlyRule(t *testing.T) {
	sf := newSyncFixture(t)
	// A first Friday comfortably in the past, so an unexpanded feed would
	// publish a date that has already been and gone.
	past := time.Now().AddDate(0, -8, 0)
	first := time.Date(past.Year(), past.Month(), 1, 18, 0, 0, 0, time.UTC)
	for first.Weekday() != time.Friday {
		first = first.AddDate(0, 0, 1)
	}
	e := sf.create(t, CreateParams{
		Title: "First Friday", StartsAt: first.Format("2006-01-02 15:04"),
		Visibility: VisibilityPublic, RRule: "FREQ=MONTHLY;BYDAY=1FR",
	})
	sf.publish(t, e)

	items := sf.feed(t).Events
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	up := items[0].Upcoming
	if len(up) == 0 {
		t.Fatal("a monthly series published no upcoming dates")
	}
	// Every date must be ahead, and every one a Friday.
	for _, got := range up {
		when, err := time.Parse(time.RFC3339, got)
		if err != nil {
			t.Fatalf("unparseable date %q: %v", got, err)
		}
		if when.Before(time.Now().AddDate(0, 0, -1)) {
			t.Errorf("published a past occurrence: %s", got)
		}
		if when.Weekday() != time.Friday {
			t.Errorf("%s is a %s, not a Friday", got, when.Weekday())
		}
		if when.Day() > 7 {
			t.Errorf("%s is not the FIRST Friday of its month", got)
		}
	}
}

// Absent, not an empty array, so the site has no special case to write --
// starts_at already says when a one-off happens.
func TestFeedOmitsUpcomingForAOneOff(t *testing.T) {
	sf := newSyncFixture(t)
	e := sf.create(t, CreateParams{
		Title: "Single Night", StartsAt: "2026-09-04 18:00", Visibility: VisibilityPublic,
	})
	sf.publish(t, e)

	items := sf.feed(t).Events
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Upcoming != nil {
		t.Errorf("a one-off should carry no expansion, got %v", items[0].Upcoming)
	}
	blob, err := json.Marshal(items[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(blob), `"upcoming"`) {
		t.Errorf("the upcoming key should be absent entirely: %s", blob)
	}
}
