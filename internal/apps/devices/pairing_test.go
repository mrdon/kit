package devices

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/testdb"
)

// The whole handshake, driven through the real routes: a device opens the
// page and polls; an admin approves from the console API; the device's next
// poll becomes a device session that the session middleware admits.

type fixture struct {
	t      *testing.T
	ctx    context.Context
	pool   *pgxpool.Pool
	app    *App
	mux    *http.ServeMux
	tenant *models.Tenant
	admin  *http.Cookie
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := testdb.Open(t)
	ctx := context.Background()
	teamID := "T_devices_" + uuid.NewString()
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "Pairing Test", "enc", models.SanitizeSlug("devices-"+uuid.NewString(), teamID), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID) })
	signer, err := auth.NewSessionSigner("devices-test-secret-with-plenty-of-entropy")
	if err != nil {
		t.Fatal(err)
	}
	app := &App{pool: pool, signer: signer, limiter: newIPLimiter()}
	mux := http.NewServeMux()
	app.RegisterRoutes(mux)

	user, err := models.GetOrCreateUser(ctx, pool, tenant.ID, "U_"+uuid.NewString()[:8], "Admin", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := models.GetOrCreateRole(ctx, pool, tenant.ID, models.RoleAdmin, ""); err != nil {
		t.Fatal(err)
	}
	if err := models.AssignRole(ctx, pool, tenant.ID, user.ID, models.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	if err := signer.Issue(ctx, rec, pool, tenant.ID, user.ID, "/"+tenant.Slug+"/"); err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, ctx: ctx, pool: pool, app: app, mux: mux, tenant: tenant, admin: cookieNamed(rec, auth.SessionCookieName)}
}

func cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name && c.Value != "" {
			return c
		}
	}
	return nil
}

// openPairPage is the device opening /pair: returns its pairing cookie and
// the row behind it.
func (f *fixture) openPairPage(ip string) (*http.Cookie, *Pairing) {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/"+f.tenant.Slug+"/pair", nil)
	req.RemoteAddr = ip + ":1234"
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		f.t.Fatalf("pair page: status %d: %s", rec.Code, rec.Body.String())
	}
	c := cookieNamed(rec, pairCookieName)
	if c == nil {
		f.t.Fatal("pair page set no cookie")
	}
	pending, err := pendingPairings(f.ctx, f.pool, f.tenant.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	for i := range pending {
		if strings.Contains(rec.Body.String(), pending[i].UserCode) {
			return c, &pending[i]
		}
	}
	f.t.Fatal("page shows no pending pairing's code")
	return nil, nil
}

func (f *fixture) poll(c *http.Cookie) (map[string]string, *http.Cookie) {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/"+f.tenant.Slug+"/pair/status", nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		f.t.Fatalf("poll: %d %s", rec.Code, rec.Body.String())
	}
	return body, cookieNamed(rec, auth.SessionCookieName)
}

func (f *fixture) adminJSON(method, path string, body any) (int, map[string]any) {
	f.t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(method, "/"+f.tenant.Slug+path, strings.NewReader(string(raw)))
	req.Header.Set(auth.CSRFHeader, "1")
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(f.admin)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// deviceCaller runs the device's cookie through the session middleware on
// an opted-in route and returns what it resolved to.
func (f *fixture) deviceCaller(c *http.Cookie) *services.Caller {
	f.t.Helper()
	var got *services.Caller
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = auth.CallerFromContext(r.Context()) })
	req := httptest.NewRequest(http.MethodGet, "/"+f.tenant.Slug+"/api/x", nil)
	req.SetPathValue("slug", f.tenant.Slug)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	auth.AllowDeviceCallers(f.app.signer.Middleware(f.pool, next)).ServeHTTP(rec, req)
	return got
}

func TestPairingHappyPath(t *testing.T) {
	f := newFixture(t)
	cookie, pairing := f.openPairPage("10.0.0.1")

	if body, _ := f.poll(cookie); body["status"] != "pending" {
		t.Fatalf("poll before approval = %v", body)
	}

	// The approver's list offers three pictures, one of them the real one.
	code, list := f.adminJSON(http.MethodGet, "/api/devices", nil)
	if code != http.StatusOK {
		t.Fatalf("list: %d", code)
	}
	pending := list["pending"].([]any)
	if len(pending) != 1 {
		t.Fatalf("pending = %v", pending)
	}
	pics := pending[0].(map[string]any)["pictures"].([]any)
	found := false
	for _, p := range pics {
		if p == pairing.Picture {
			found = true
		}
	}
	if len(pics) != 3 || !found {
		t.Fatalf("pictures = %v, want 3 including %s", pics, pairing.Picture)
	}

	code, out := f.adminJSON(http.MethodPost, "/api/devices/pairings/approve", map[string]any{
		"pairing_id": pairing.ID.String(), "picture": pairing.Picture,
		"label": "Trivia laptop", "capabilities": []string{auth.CapTriviaHost},
	})
	if code != http.StatusOK {
		t.Fatalf("approve: %d %v", code, out)
	}

	// The device's next poll is the only place the session is handed out.
	body, session := f.poll(cookie)
	if body["status"] != "approved" || session == nil || !strings.HasPrefix(body["redirect"], "/"+f.tenant.Slug+"/web") {
		t.Fatalf("poll after approval = %v, session = %v", body, session)
	}
	if session.Path != "/"+f.tenant.Slug+"/" {
		t.Fatalf("session cookie path = %q", session.Path)
	}
	caller := f.deviceCaller(session)
	if caller == nil || caller.Kind != services.CallerDevice || caller.Label != "Trivia laptop" || !caller.HasCapability(auth.CapTriviaHost) {
		t.Fatalf("device caller = %+v", caller)
	}
	if caller.HasCapability(auth.CapMenuHappyHour) {
		t.Fatal("device holds a capability it was not given")
	}

	// A second poll finds nothing: the pairing was consumed.
	if body, _ := f.poll(cookie); body["status"] != "gone" {
		t.Fatalf("second poll = %v", body)
	}
}

func TestWrongPictureCancelsThePairing(t *testing.T) {
	f := newFixture(t)
	cookie, pairing := f.openPairPage("10.0.0.2")
	wrong := pictures[0]
	if wrong == pairing.Picture {
		wrong = pictures[1]
	}
	code, _ := f.adminJSON(http.MethodPost, "/api/devices/pairings/approve", map[string]any{
		"pairing_id": pairing.ID.String(), "picture": wrong,
		"label": "Someone's laptop", "capabilities": []string{auth.CapTriviaHost},
	})
	if code != http.StatusConflict {
		t.Fatalf("wrong picture: %d, want 409", code)
	}
	if body, session := f.poll(cookie); body["status"] != "gone" || session != nil {
		t.Fatalf("poll after wrong pick = %v, session = %v", body, session)
	}
	// And it can't be approved afterwards with the right one.
	code, _ = f.adminJSON(http.MethodPost, "/api/devices/pairings/approve", map[string]any{
		"pairing_id": pairing.ID.String(), "picture": pairing.Picture,
		"label": "Someone's laptop", "capabilities": []string{auth.CapTriviaHost},
	})
	if code != http.StatusGone {
		t.Fatalf("approve after cancel: %d, want 410", code)
	}
}

func TestApproveByCode(t *testing.T) {
	f := newFixture(t)
	cookie, pairing := f.openPairPage("10.0.0.3")
	code, _ := f.adminJSON(http.MethodPost, "/api/devices/pairings/approve", map[string]any{
		"code":  strings.ToLower(pairing.UserCode),
		"label": "Bar iPad", "capabilities": []string{auth.CapMenuHappyHour, auth.CapMenuPrint},
	})
	if code != http.StatusOK {
		t.Fatalf("approve by code: %d", code)
	}
	body, session := f.poll(cookie)
	if body["status"] != "approved" || session == nil {
		t.Fatalf("poll = %v", body)
	}
	caller := f.deviceCaller(session)
	if caller == nil || !caller.HasCapability(auth.CapMenuPrint) || caller.HasCapability(auth.CapTriviaHost) {
		t.Fatalf("caller = %+v", caller)
	}
}

func TestApproveRefusesBadInput(t *testing.T) {
	f := newFixture(t)
	_, pairing := f.openPairPage("10.0.0.4")
	for name, body := range map[string]map[string]any{
		"no label":           {"pairing_id": pairing.ID.String(), "picture": pairing.Picture, "capabilities": []string{auth.CapTriviaHost}},
		"no capabilities":    {"pairing_id": pairing.ID.String(), "picture": pairing.Picture, "label": "x"},
		"unknown capability": {"pairing_id": pairing.ID.String(), "picture": pairing.Picture, "label": "x", "capabilities": []string{"root.everything"}},
	} {
		if code, _ := f.adminJSON(http.MethodPost, "/api/devices/pairings/approve", body); code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", name, code)
		}
	}
	if code, _ := f.adminJSON(http.MethodPost, "/api/devices/pairings/approve", map[string]any{
		"code": "ZZZZ", "label": "x", "capabilities": []string{auth.CapTriviaHost},
	}); code != http.StatusNotFound {
		t.Errorf("unknown code: %d, want 404", code)
	}
}

func TestRevokeEndsTheSession(t *testing.T) {
	f := newFixture(t)
	cookie, pairing := f.openPairPage("10.0.0.5")
	_, out := f.adminJSON(http.MethodPost, "/api/devices/pairings/approve", map[string]any{
		"pairing_id": pairing.ID.String(), "picture": pairing.Picture,
		"label": "Trivia laptop", "capabilities": []string{auth.CapTriviaHost},
	})
	_, session := f.poll(cookie)
	if f.deviceCaller(session) == nil {
		t.Fatal("device not admitted before revoke")
	}
	id := out["device"].(map[string]any)["id"].(string)

	// Edit first: the label and capabilities change in place.
	code, edited := f.adminJSON(http.MethodPatch, "/api/devices/"+id, map[string]any{
		"label": "Quiz machine", "capabilities": []string{auth.CapTriviaHost, auth.CapKioskRepoint},
	})
	if code != http.StatusOK {
		t.Fatalf("update: %d %v", code, edited)
	}
	if c := f.deviceCaller(session); c == nil || c.Label != "Quiz machine" || !c.HasCapability(auth.CapKioskRepoint) {
		t.Fatalf("after edit: %+v", c)
	}

	if code, _ := f.adminJSON(http.MethodDelete, "/api/devices/"+id, nil); code != http.StatusNoContent {
		t.Fatalf("revoke: %d", code)
	}
	if f.deviceCaller(session) != nil {
		t.Fatal("revoked device still admitted")
	}
}

func TestPairPageReloadKeepsThePairing(t *testing.T) {
	f := newFixture(t)
	cookie, pairing := f.openPairPage("10.0.0.6")
	req := httptest.NewRequest(http.MethodGet, "/"+f.tenant.Slug+"/pair", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), pairing.UserCode) {
		t.Fatal("reload changed the code")
	}
	n, _ := countPending(f.ctx, f.pool, f.tenant.ID)
	if n != 1 {
		t.Fatalf("pending = %d after reload, want 1", n)
	}
}

func TestPairingRateLimitPerIP(t *testing.T) {
	f := newFixture(t)
	for range pairingsPerIP {
		f.openPairPage("10.0.0.7")
	}
	req := httptest.NewRequest(http.MethodGet, "/"+f.tenant.Slug+"/pair", nil)
	req.RemoteAddr = "10.0.0.7:1"
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	// A different address is unaffected.
	f.openPairPage("10.0.0.8")
}

func TestExpiredPairingIsGone(t *testing.T) {
	f := newFixture(t)
	cookie, pairing := f.openPairPage("10.0.0.9")
	if _, err := f.pool.Exec(f.ctx, `UPDATE app_device_pairings SET expires_at = $2 WHERE id = $1`, pairing.ID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if body, _ := f.poll(cookie); body["status"] != "gone" {
		t.Fatalf("poll = %v", body)
	}
	if code, _ := f.adminJSON(http.MethodPost, "/api/devices/pairings/approve", map[string]any{
		"pairing_id": pairing.ID.String(), "picture": pairing.Picture, "label": "x", "capabilities": []string{auth.CapTriviaHost},
	}); code != http.StatusGone {
		t.Fatalf("approve expired: %d, want 410", code)
	}
}

func TestChoicesAreStableAndIncludeTheAnswer(t *testing.T) {
	p := &Pairing{ID: uuid.New(), Picture: pictures[7]}
	a, b := p.choices(), p.choices()
	if len(a) != 3 || strings.Join(a, "") != strings.Join(b, "") {
		t.Fatalf("choices not stable: %v vs %v", a, b)
	}
	seen := map[string]bool{}
	for _, pic := range a {
		seen[pic] = true
	}
	if !seen[p.Picture] || len(seen) != 3 {
		t.Fatalf("choices = %v", a)
	}
}

// A kiosk that boots to /pair must not pair itself again every morning:
// with a device session already in the jar, the page sends it home.
func TestPairPageSendsPairedDeviceHome(t *testing.T) {
	f := newFixture(t)
	cookie, pairing := f.openPairPage("10.0.0.10")
	f.adminJSON(http.MethodPost, "/api/devices/pairings/approve", map[string]any{
		"pairing_id": pairing.ID.String(), "picture": pairing.Picture,
		"label": "Trivia laptop", "capabilities": []string{auth.CapTriviaHost},
	})
	_, session := f.poll(cookie)
	if session == nil {
		t.Fatal("no session issued")
	}
	before, _ := countPending(f.ctx, f.pool, f.tenant.ID)

	req := httptest.NewRequest(http.MethodGet, "/"+f.tenant.Slug+"/pair", nil)
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther || !strings.HasSuffix(rec.Header().Get("Location"), "/web/") {
		t.Fatalf("status = %d, location = %q; want a redirect home", rec.Code, rec.Header().Get("Location"))
	}
	if after, _ := countPending(f.ctx, f.pool, f.tenant.ID); after != before {
		t.Fatalf("a paired device opening /pair created a pairing (%d -> %d)", before, after)
	}
}

// The start link is the kiosk's answer to a browser that wipes its cookies:
// opening it signs that browser in as the device, every boot. Making a new
// one ends the old; unpairing ends both.
func TestStartLink(t *testing.T) {
	f := newFixture(t)
	cookie, pairing := f.openPairPage("10.0.0.11")
	_, out := f.adminJSON(http.MethodPost, "/api/devices/pairings/approve", map[string]any{
		"pairing_id": pairing.ID.String(), "picture": pairing.Picture,
		"label": "Trivia laptop", "capabilities": []string{auth.CapTriviaHost},
	})
	f.poll(cookie)
	id := out["device"].(map[string]any)["id"].(string)

	code, made := f.adminJSON(http.MethodPost, "/api/devices/"+id+"/start-link", nil)
	if code != http.StatusOK {
		t.Fatalf("make start link: %d %v", code, made)
	}
	startURL, _ := made["start_url"].(string)
	if !strings.Contains(startURL, "/"+f.tenant.Slug+"/device/"+startTokenPrefix) || made["qr_svg"] == nil {
		t.Fatalf("start link = %q, qr present = %v", startURL, made["qr_svg"] != nil)
	}
	_, list := f.adminJSON(http.MethodGet, "/api/devices", nil)
	if dev := list["devices"].([]any)[0].(map[string]any); dev["has_start_link"] != true {
		t.Fatalf("list does not show the start link: %v", dev)
	}

	open := func(url string) (*httptest.ResponseRecorder, *http.Cookie) {
		path := url[strings.Index(url, "/"+f.tenant.Slug+"/device/"):]
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "10.0.0.12:1"
		rec := httptest.NewRecorder()
		f.mux.ServeHTTP(rec, req)
		return rec, cookieNamed(rec, auth.SessionCookieName)
	}
	rec, session := open(startURL)
	if rec.Code != http.StatusSeeOther || session == nil || !strings.HasSuffix(rec.Header().Get("Location"), "/web/") {
		t.Fatalf("start link: %d %q session=%v", rec.Code, rec.Header().Get("Location"), session != nil)
	}
	if c := f.deviceCaller(session); c == nil || c.Label != "Trivia laptop" || !c.HasCapability(auth.CapTriviaHost) {
		t.Fatalf("caller from start link = %+v", c)
	}

	// A second boot works too: the link is reusable, the cookie is per session.
	if rec, s2 := open(startURL); rec.Code != http.StatusSeeOther || s2 == nil {
		t.Fatalf("second boot: %d", rec.Code)
	}

	// Rotating invalidates the old link.
	_, remade := f.adminJSON(http.MethodPost, "/api/devices/"+id+"/start-link", nil)
	if rec, _ := open(startURL); rec.Code != http.StatusNotFound {
		t.Fatalf("old link after rotate: %d, want 404", rec.Code)
	}
	newURL := remade["start_url"].(string)
	if rec, _ := open(newURL); rec.Code != http.StatusSeeOther {
		t.Fatalf("new link: %d", rec.Code)
	}

	// Unpairing ends the link and every session it issued.
	f.adminJSON(http.MethodDelete, "/api/devices/"+id, nil)
	if rec, _ := open(newURL); rec.Code != http.StatusNotFound {
		t.Fatalf("link after unpair: %d, want 404", rec.Code)
	}
	if f.deviceCaller(session) != nil {
		t.Fatal("session from the link survived unpairing")
	}
}

// A kiosk is created by name and gets its link at once; no pairing ever.
func TestCreateKioskWithoutPairing(t *testing.T) {
	f := newFixture(t)
	code, out := f.adminJSON(http.MethodPost, "/api/devices", map[string]any{
		"label": "Quiz laptop", "capabilities": []string{auth.CapTriviaHost},
	})
	if code != http.StatusOK {
		t.Fatalf("create kiosk: %d %v", code, out)
	}
	startURL, _ := out["start_url"].(string)
	dev := out["device"].(map[string]any)
	if startURL == "" || dev["has_start_link"] != true || dev["label"] != "Quiz laptop" {
		t.Fatalf("create kiosk = %v", out)
	}
	path := startURL[strings.Index(startURL, "/"+f.tenant.Slug+"/device/"):]
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.RemoteAddr = "10.0.0.13:1"
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	session := cookieNamed(rec, auth.SessionCookieName)
	if rec.Code != http.StatusSeeOther || session == nil {
		t.Fatalf("boot from link: %d", rec.Code)
	}
	if c := f.deviceCaller(session); c == nil || c.Label != "Quiz laptop" {
		t.Fatalf("caller = %+v", c)
	}
	if n, _ := countPending(f.ctx, f.pool, f.tenant.ID); n != 0 {
		t.Fatalf("a pairing row appeared: %d", n)
	}
	if code, _ := f.adminJSON(http.MethodPost, "/api/devices", map[string]any{"label": "x"}); code != http.StatusBadRequest {
		t.Fatalf("no capabilities: %d, want 400", code)
	}
}
