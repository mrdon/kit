package hoppers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mrdon/kit/internal/apps/console"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/models"
)

// consoleMux serves the host API through the real session and capability
// chain, so a device's cookie is judged exactly as in production.
func (f *fixture) consoleMux(signer *auth.SessionSigner) *http.ServeMux {
	mux := http.NewServeMux()
	registerConsoleRoutes(mux, &App{pool: f.pool, svc: f.svc, signer: signer, baseURL: "http://localhost:8489"})
	return mux
}

func (f *fixture) deviceCookie(signer *auth.SessionSigner, caps ...string) *http.Cookie {
	f.t.Helper()
	actor, err := models.CreateActor(f.ctx, f.pool, f.tenant.ID, models.ActorKindDevice, "Taproom iPad", caps, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	if err := signer.IssueDevice(f.ctx, rec, f.pool, f.tenant.ID, actor.ID, actor.Label, "/"+f.tenant.Slug+"/"); err != nil {
		f.t.Fatal(err)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookieName && c.Value != "" {
			return c
		}
	}
	f.t.Fatal("no session cookie")
	return nil
}

// The taproom iPad hosts with hoppers.host and nothing else; a device without
// it is refused. The host's flow end to end: create, advance, a double tap
// refused, a reclaim code issued.
func TestTheHostAPIOnADevice(t *testing.T) {
	f := newFixture(t)
	signer, err := auth.NewSessionSigner("hoppers-test-secret-with-plenty-of-entropy")
	if err != nil {
		t.Fatal(err)
	}
	mux := f.consoleMux(signer)
	call := func(c *http.Cookie, method, path string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(method, "/"+f.tenant.Slug+"/api/hoppers"+path, bytes.NewReader(raw))
		req.Header.Set(console.CSRFHeader, "1")
		req.AddCookie(c)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	if rec := call(f.deviceCookie(signer, auth.CapTriviaHost), http.MethodGet, "/games", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("a trivia-only device listed Hoppers games: %d", rec.Code)
	}
	ipad := f.deviceCookie(signer, auth.CapHoppersHost)

	rec := call(ipad, http.MethodPost, "/games", NewGame{Pack: "default"})
	if rec.Code != http.StatusOK {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var g gameJSON
	_ = json.Unmarshal(rec.Body.Bytes(), &g)
	game, err := GetGame(f.ctx, f.pool, f.tenant.ID, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	ann := f.join(game, "Ann")

	from := stageOf(f.snap(game).TV)
	if rec := call(ipad, http.MethodPost, "/games/"+g.ID.String()+"/advance", from); rec.Code != http.StatusOK {
		t.Fatalf("advance: %d %s", rec.Code, rec.Body)
	}
	if rec := call(ipad, http.MethodPost, "/games/"+g.ID.String()+"/advance", from); rec.Code != http.StatusConflict {
		t.Fatalf("a double tap got %d, want 409", rec.Code)
	}
	rec = call(ipad, http.MethodPost, "/games/"+g.ID.String()+"/players/"+ann.ID.String()+"/reclaim", nil)
	var code struct{ Code string }
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &code) != nil || len(code.Code) != 4 {
		t.Fatalf("reclaim: %d %s", rec.Code, rec.Body)
	}
	if rec := call(ipad, http.MethodGet, "/packs", nil); rec.Code != http.StatusOK || !bytes.Contains(rec.Body.Bytes(), []byte(PackGenerated)) {
		t.Fatalf("packs: %d %s", rec.Code, rec.Body)
	}
}
