package hoppers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mrdon/kit/internal/livegame"
)

// serveMux mounts the public routes beside a stand-in for the cards PWA's
// GET /{slug}/ catch-all, so requests route exactly as in production.
func (f *fixture) serveMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{slug}/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Served-By", "cards-catch-all")
	})
	registerPublicRoutes(mux, &App{pool: f.pool, svc: f.svc, baseURL: "http://localhost:8489"})
	return mux
}

func (f *fixture) request(method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
	f.t.Helper()
	raw := []byte(nil)
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	f.serveMux().ServeHTTP(rec, req)
	return rec
}

func (f *fixture) gamePath(g *Game) string { return JoinURL(f.tenant.Slug, g.Name) }

func (f *fixture) joinOverHTTP(g *Game, name string) *http.Cookie {
	f.t.Helper()
	rec := f.request(http.MethodPost, f.gamePath(g)+"/join", Frog{Name: name}, nil)
	if rec.Code != http.StatusOK {
		f.t.Fatalf("join: %d %s", rec.Code, rec.Body)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == PlayerCookieName {
			return c
		}
	}
	f.t.Fatal("join set no cookie")
	return nil
}

func frameOfBody(t *testing.T, rec *httptest.ResponseRecorder) Frame {
	t.Helper()
	var fr Frame
	if err := json.Unmarshal(rec.Body.Bytes(), &fr); err != nil {
		t.Fatalf("decoding frame: %v\n%s", err, rec.Body)
	}
	return fr
}

func TestJoinSetsAGameScopedCookie(t *testing.T) {
	f := newFixture(t)
	g := f.newGame()
	c := f.joinOverHTTP(g, "Ann")
	if c.Path != CookiePath(f.tenant.Slug, g.Name) || !c.HttpOnly {
		t.Fatalf("cookie = %+v", c)
	}
	id, _, ok := livegame.ParseCookieValue(c.Value)
	if !ok {
		t.Fatal("the cookie does not parse")
	}
	fr := frameOfBody(t, f.request(http.MethodGet, f.gamePath(g)+"/state", nil, c))
	if fr.You == nil || fr.You.ID != id.String() {
		t.Fatalf("the joined phone's frame has You = %+v", fr.You)
	}
	if fr.Game != g.ID.String() || fr.Version == 0 {
		t.Fatalf("frame transport fields = game %q version %d", fr.Game, fr.Version)
	}
}

// No cookie, or one that does not resolve, is a spectator: the full read-only
// view, never a 401.
func TestSpectatorsGetTheReadOnlyView(t *testing.T) {
	f := newFixture(t)
	g := f.newGame()
	c := f.joinOverHTTP(g, "Ann")
	id, _, _ := livegame.ParseCookieValue(c.Value)
	tampered := &http.Cookie{Name: PlayerCookieName, Value: livegame.CookieValue(id, "not-the-token")}
	for name, cookie := range map[string]*http.Cookie{"none": nil, "tampered": tampered} {
		rec := f.request(http.MethodGet, f.gamePath(g)+"/state", nil, cookie)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", name, rec.Code)
		}
		if fr := frameOfBody(t, rec); fr.You != nil || len(fr.Players) == 0 {
			t.Fatalf("%s: You = %+v, %d players", name, fr.You, len(fr.Players))
		}
	}
	if rec := f.request(http.MethodPost, f.gamePath(g)+"/flick", flickRequest{Power: 0.5}, tampered); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a spectator's flick got %d", rec.Code)
	}
}

func TestPollAnswers204WhenNothingIsNew(t *testing.T) {
	f := newFixture(t)
	g := f.newGame()
	fr := frameOfBody(t, f.request(http.MethodGet, f.gamePath(g)+"/tv/state", nil, nil))
	rec := f.request(http.MethodGet, f.gamePath(g)+"/tv/state?since="+itoa(fr.Version), nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("poll at the current version: %d", rec.Code)
	}
}

func TestReclaimOverHTTP(t *testing.T) {
	f := newFixture(t)
	g := f.newGame()
	old := f.joinOverHTTP(g, "Ann")
	id, _, _ := livegame.ParseCookieValue(old.Value)
	code := livegame.ReclaimCode()
	if err := f.svc.IssueReclaim(f.ctx, f.tenant.ID, g.ID, id, code); err != nil {
		t.Fatal(err)
	}
	if rec := f.request(http.MethodPost, f.gamePath(g)+"/reclaim", reclaimRequest{PlayerID: id.String(), Code: "0000x"}, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("a wrong code got %d", rec.Code)
	}
	rec := f.request(http.MethodPost, f.gamePath(g)+"/reclaim", reclaimRequest{PlayerID: id.String(), Code: code}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("reclaim: %d %s", rec.Code, rec.Body)
	}
	var fresh *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == PlayerCookieName {
			fresh = c
		}
	}
	if fresh == nil {
		t.Fatal("reclaim set no cookie")
	}
	if fr := frameOfBody(t, f.request(http.MethodGet, f.gamePath(g)+"/state", nil, fresh)); fr.You == nil || fr.You.ID != id.String() {
		t.Fatal("the new phone is not the frog")
	}
	if fr := frameOfBody(t, f.request(http.MethodGet, f.gamePath(g)+"/state", nil, old)); fr.You != nil {
		t.Fatal("the lost phone is still the frog")
	}
}

func TestShortJoinRedirectsToTheGame(t *testing.T) {
	f := newFixture(t)
	g := f.newGame()
	rec := f.request(http.MethodGet, "/j/h/"+g.JoinCode, nil, nil)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != f.gamePath(g) {
		t.Fatalf("short join: %d -> %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := f.request(http.MethodGet, "/j/h/zzzzz", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown code: %d", rec.Code)
	}
}

// The TV is one self-contained page: the join QR, the shared live client and
// the game's stream URL all inlined. The stable address follows the newest
// game, and says so plainly before there is one.
func TestTVPages(t *testing.T) {
	f := newFixture(t)
	latest := "/" + f.tenant.Slug + "/hoppers/tv"
	if rec := f.request(http.MethodGet, latest, nil, nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "No game yet") {
		t.Fatalf("no-game TV: %d", rec.Code)
	}
	g := f.newGame()
	for _, path := range []string{latest, f.gamePath(g) + "/tv"} {
		rec := f.request(http.MethodGet, path, nil, nil)
		body := rec.Body.String()
		if rec.Code != http.StatusOK || rec.Header().Get("X-Served-By") != "" {
			t.Fatalf("%s: %d (served by %q)", path, rec.Code, rec.Header().Get("X-Served-By"))
		}
		// html/template writes "/" as "\/" inside a script string
		stream := strings.ReplaceAll(f.gamePath(g)+"/tv/stream", "/", `\/`)
		for _, want := range []string{"KitLive", "<svg", stream, "/j/h/" + g.JoinCode} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: page is missing %q", path, want)
			}
		}
		if rec.Header().Get("Cache-Control") == "" {
			t.Errorf("%s: the TV page is cacheable", path)
		}
	}
	rec := f.request(http.MethodGet, latest+".version", nil, nil)
	if rec.Code != http.StatusOK || rec.Body.String() != displayVersion(g) {
		t.Fatalf("tv.version = %d %q", rec.Code, rec.Body)
	}
}

func itoa(n uint64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
