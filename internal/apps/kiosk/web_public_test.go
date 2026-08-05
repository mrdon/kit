package kiosk

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// serveBoard drives a real request through the same mux + tenant middleware
// the app registers in production, so the {slug}/{key} routing and the tenant
// lookup are exercised rather than stubbed.
func (f *fixture) serveBoard(t *testing.T, key string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	registerPublicRoutes(mux, &App{pool: f.pool, svc: f.svc})
	req := httptest.NewRequest(http.MethodGet, "/"+f.tenant.Slug+"/kiosk/"+key, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// The central contract: the poller reads Location without following it, so
// the status must be a 302 (never a cacheable 301) and the response must
// forbid caching. A cached redirect pins a screen to stale content with no
// way for an admin to tell.
func TestBoardRedirects(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.Create(f.ctx, f.tenant.ID, BoardInput{Name: "Lobby", URL: "https://example.com/dash"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	rec := f.serveBoard(t, "lobby")

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "https://example.com/dash" {
		t.Fatalf("Location = %q", got)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Fatalf("Cache-Control = %q, want no-store", cc)
	}
}

// Changing the URL in the console is the whole feature; the next poll must
// see the new target.
func TestBoardRedirectFollowsURLChange(t *testing.T) {
	f := newFixture(t)
	b, err := f.svc.Create(f.ctx, f.tenant.ID, BoardInput{Name: "Lobby", URL: "https://old.example.com"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := f.svc.Update(f.ctx, f.tenant.ID, b.ID, BoardInput{
		Key: b.Key, Name: b.Name, URL: "https://new.example.com",
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got := f.serveBoard(t, "lobby").Header().Get("Location"); got != "https://new.example.com" {
		t.Fatalf("Location = %q, want the updated URL", got)
	}
}

// An unassigned board answers 200 with a readable page and NO Location, so a
// poller leaves the screen alone rather than navigating it somewhere.
func TestUnassignedBoardServesPlaceholder(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.Create(f.ctx, f.tenant.ID, BoardInput{Name: "Brewhouse"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	rec := f.serveBoard(t, "brewhouse")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Fatalf("Location = %q, want none", loc)
	}
	if !strings.Contains(rec.Body.String(), "Brewhouse") {
		t.Fatal("placeholder should name the board so an admin can identify the screen")
	}
}

// The board name lands in HTML, so it goes through escaping.
func TestPlaceholderEscapesName(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.Create(f.ctx, f.tenant.ID, BoardInput{
		Name: `Lobby <script>alert(1)</script>`, Key: "lobby",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	body := f.serveBoard(t, "lobby").Body.String()
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Fatal("board name was interpolated into the placeholder unescaped")
	}
}

func TestUnknownBoardIs404(t *testing.T) {
	f := newFixture(t)
	if rec := f.serveBoard(t, "nope"); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// Polling is the only heartbeat the design affords, so the public GET must
// record it.
func TestPollRecordsLastSeen(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.Create(f.ctx, f.tenant.ID, BoardInput{Name: "Lobby", URL: "https://example.com"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	f.serveBoard(t, "lobby")

	b, err := f.svc.Resolve(f.ctx, f.tenant.ID, "lobby")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if b.LastSeenAt == nil {
		t.Fatal("a public poll should record last_seen_at")
	}
}
