package trivia

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// explainerRequest serves one request through the public routes with an app
// whose video cache points at the fake Drive.
func (f *fixture) explainerRequest(cache *explainerCache, req *http.Request) *httptest.ResponseRecorder {
	f.t.Helper()
	mux := http.NewServeMux()
	registerPublicRoutes(mux, &App{pool: f.pool, svc: f.svc, baseURL: "http://localhost:8489", explainer: cache})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestExplainerVideoRoute(t *testing.T) {
	f := newFixture(t)
	_, srv := newFakeDrive(t)
	cache, _ := testCache(t, srv, "")
	path := "/" + f.tenant.Slug + "/trivia/explainer.mp4"

	// No link set: nothing to serve, and nothing asked of Drive.
	if rec := f.explainerRequest(cache, httptest.NewRequest(http.MethodGet, path, nil)); rec.Code != http.StatusNotFound {
		t.Fatalf("with no link: got %d, want 404", rec.Code)
	}

	if err := SaveExplainerLink(f.ctx, f.pool, f.tenant.ID, "https://drive.google.com/file/d/"+testDriveID+"/view"); err != nil {
		t.Fatalf("saving link: %v", err)
	}
	rec := f.explainerRequest(cache, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "video-bytes-v1" {
		t.Fatalf("got %d %q, want the video", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Errorf("Content-Type = %q", ct)
	}

	// A <video> seeks with range requests; a server that ignores them makes
	// a TV re-download the file to loop it.
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Range", "bytes=6-10")
	if rec := f.explainerRequest(cache, req); rec.Code != http.StatusPartialContent || rec.Body.String() != "bytes" {
		t.Errorf("range request: got %d %q, want 206 \"bytes\"", rec.Code, rec.Body.String())
	}
}

// The lobby plays the video only when one is set, and setting one changes
// the version so a screen already on the wall reloads to show it.
func TestLobbyShowsExplainerWhenSet(t *testing.T) {
	f := newFixture(t)
	game := f.newGame(defaultSettings(), nil)
	versionPath := "/" + f.tenant.Slug + "/trivia/tv.version"

	plain := f.request(http.MethodGet, f.gamePath(game)+"/tv", nil, nil).Body.String()
	if strings.Contains(plain, `id="join-video"`) || strings.Contains(plain, `class="join with-video"`) {
		t.Fatal("the lobby has a video with none set")
	}
	before := f.request(http.MethodGet, versionPath, nil, nil).Body.String()

	if err := SaveExplainerLink(f.ctx, f.pool, f.tenant.ID, testDriveID); err != nil {
		t.Fatalf("saving link: %v", err)
	}
	page := f.request(http.MethodGet, f.gamePath(game)+"/tv", nil, nil).Body.String()
	if !strings.Contains(page, `src="/`+f.tenant.Slug+`/trivia/explainer.mp4"`) || !strings.Contains(page, `class="join with-video"`) {
		t.Fatal("the lobby does not play the video once one is set")
	}
	if after := f.request(http.MethodGet, versionPath, nil, nil).Body.String(); after == before {
		t.Errorf("setting a video left the version at %q; open screens would never show it", after)
	}
}
