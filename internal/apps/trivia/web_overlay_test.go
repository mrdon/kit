package trivia

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

var versionStamp = regexp.MustCompile(`__KIT_TRIVIA_VERSION__ = "([^"]*)"`)

func stampOf(t *testing.T, page string) string {
	t.Helper()
	m := versionStamp.FindStringSubmatch(page)
	if m == nil {
		t.Fatal("page carries no version stamp")
	}
	return m[1]
}

// The overlay is the TV page in another shape, so it must be just as
// self-contained, and it must say it is the overlay or core.js draws the wall
// into a page that has none of the wall's markup.
func TestOverlayPageIsSelfContainedAndTransparent(t *testing.T) {
	f := newFixture(t)
	game := f.newGame(defaultSettings(), nil)
	rec := f.request(http.MethodGet, f.gamePath(game)+"/tv?overlay=1", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("overlay returned %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"__KIT_TRIVIA_OVERLAY__ = true", "data:font/woff2;base64,", "<svg",
		"__KIT_TRIVIA_STREAM__", "function renderOverlay", "background: transparent"} {
		if !strings.Contains(body, want) {
			t.Fatalf("overlay page is missing %q", want)
		}
	}
	for _, bad := range []string{"ZgotmplZ", "/console/assets/", `http-equiv="refresh"`} {
		if strings.Contains(body, bad) {
			t.Fatalf("overlay page contains %q", bad)
		}
	}
	wall := f.request(http.MethodGet, f.gamePath(game)+"/tv", nil, nil).Body.String()
	if strings.Contains(wall, "__KIT_TRIVIA_OVERLAY__ = true") {
		t.Fatal("the wall was served as the overlay")
	}
}

// The overlay polls the same tv.version as the wall and reloads when its
// stamp differs. A stamp that left anything out -- the explainer link is the
// easy one to miss, since the overlay never shows the video -- would never
// match, and the overlay would reload every fifteen seconds all night.
func TestOverlayStampMatchesTVVersion(t *testing.T) {
	f := newFixture(t)
	game := f.newGame(defaultSettings(), nil)
	if err := SaveExplainerLink(f.ctx, f.pool, f.tenant.ID, testDriveID); err != nil {
		t.Fatalf("saving link: %v", err)
	}
	for _, path := range []string{f.gamePath(game) + "/tv", "/" + f.tenant.Slug + "/trivia/tv"} {
		page := f.request(http.MethodGet, path+"?overlay=1", nil, nil).Body.String()
		want := f.request(http.MethodGet, path+".version", nil, nil).Body.String()
		if got := stampOf(t, page); got != want {
			t.Errorf("%s?overlay=1 stamp = %q, tv.version = %q", path, got, want)
		}
	}
}

// Before the first game the stable address must not paint an opaque card
// over the broadcast. It still polls, so the first game appears unattended.
func TestOverlayPlaceholderIsTransparent(t *testing.T) {
	f := newFixture(t)
	body := f.request(http.MethodGet, "/"+f.tenant.Slug+"/trivia/tv?overlay=1", nil, nil).Body.String()
	if strings.Contains(body, "No game yet") || strings.Contains(body, "gradient") {
		t.Fatal("the overlay placeholder paints over the broadcast")
	}
	if !strings.Contains(body, `http-equiv="refresh"`) {
		t.Fatal("the overlay placeholder never looks for the first game")
	}
}

func TestIsOverlay(t *testing.T) {
	for q, want := range map[string]bool{
		"": false, "?overlay=1": true, "?overlay=true": true, "?overlay=yes": true,
		"?overlay=0": false, "?overlay=false": false, "?overlay=": false, "?other=1": false,
	} {
		r := httptest.NewRequest(http.MethodGet, "/x/trivia/tv"+q, nil)
		if got := isOverlay(r); got != want {
			t.Errorf("isOverlay(%q) = %v, want %v", q, got, want)
		}
	}
}
