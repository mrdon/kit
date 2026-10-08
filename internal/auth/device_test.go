package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/auth/opaquetoken"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/testdb"
)

// Device sessions ride the same cookie and the same api_tokens row as a
// person's session. What makes them safe is the default deny: the session
// middleware refuses a device everywhere except routes that opted in.

func testSigner(t *testing.T) *SessionSigner {
	t.Helper()
	s, err := NewSessionSigner("device-test-secret-with-plenty-of-entropy")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func makeDevice(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, caps ...string) *models.Actor {
	t.Helper()
	actor, err := models.CreateActor(ctx, pool, tenantID, models.ActorKindDevice, "Test iPad", caps, nil)
	if err != nil {
		t.Fatalf("creating actor: %v", err)
	}
	return actor
}

// deviceCookie pairs the device and returns the cookie a browser would
// then carry.
func deviceCookie(t *testing.T, ctx context.Context, pool *pgxpool.Pool, signer *SessionSigner, tenant *models.Tenant, actor *models.Actor) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := signer.IssueDevice(ctx, rec, pool, tenant.ID, actor.ID, actor.Label, "/"+tenant.Slug+"/"); err != nil {
		t.Fatalf("IssueDevice: %v", err)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookieName && c.Value != "" {
			return c
		}
	}
	t.Fatal("no session cookie issued")
	return nil
}

func deviceRequest(tenant *models.Tenant, cookie *http.Cookie) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/"+tenant.Slug+"/api/tasks", nil)
	req.SetPathValue("slug", tenant.Slug)
	req.AddCookie(cookie)
	return req
}

func TestDeviceSessionRefusedByDefault(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	tenant := oneTenant(t, ctx, pool)
	signer := testSigner(t)
	actor := makeDevice(t, ctx, pool, tenant.ID, "trivia.host")
	cookie := deviceCookie(t, ctx, pool, signer, tenant, actor)

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called = true })
	rec := httptest.NewRecorder()
	signer.Middleware(pool, next).ServeHTTP(rec, deviceRequest(tenant, cookie))

	if rec.Code != http.StatusForbidden || called {
		t.Fatalf("status = %d, called = %v; want 403 and not called", rec.Code, called)
	}
}

func TestDeviceSessionAdmittedWhenRouteOptsIn(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	tenant := oneTenant(t, ctx, pool)
	signer := testSigner(t)
	actor := makeDevice(t, ctx, pool, tenant.ID, "trivia.host", "menu.print")
	cookie := deviceCookie(t, ctx, pool, signer, tenant, actor)

	var got *services.Caller
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = CallerFromContext(r.Context()) })
	rec := httptest.NewRecorder()
	AllowDeviceCallers(signer.Middleware(pool, next)).ServeHTTP(rec, deviceRequest(tenant, cookie))

	if rec.Code != http.StatusOK || got == nil {
		t.Fatalf("status = %d, caller = %v", rec.Code, got)
	}
	if got.Kind != services.CallerDevice || got.ActorID != actor.ID || got.Label != "Test iPad" || got.IsUser() {
		t.Fatalf("caller = %+v", got)
	}
	if !got.HasCapability("menu.print") || got.HasCapability("kiosk.repoint") {
		t.Fatalf("capabilities = %v", got.Capabilities)
	}
	if got.UserID != uuid.Nil || got.Identity != "" || got.IsAdmin || len(got.Roles) != 0 {
		t.Fatalf("device caller carries a person's fields: %+v", got)
	}
}

func TestRevokedDeviceIsNoSession(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	tenant := oneTenant(t, ctx, pool)
	signer := testSigner(t)
	actor := makeDevice(t, ctx, pool, tenant.ID, "trivia.host")
	cookie := deviceCookie(t, ctx, pool, signer, tenant, actor)

	if err := models.RevokeActor(ctx, pool, tenant.ID, actor.ID); err != nil {
		t.Fatalf("RevokeActor: %v", err)
	}

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called = true })
	rec := httptest.NewRecorder()
	AllowDeviceCallers(signer.Middleware(pool, next)).ServeHTTP(rec, deviceRequest(tenant, cookie))
	if rec.Code != http.StatusUnauthorized || called {
		t.Fatalf("status = %d, called = %v; want 401 and not called", rec.Code, called)
	}
}

func TestDeviceTokenRejectedAsBearer(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	tenant := oneTenant(t, ctx, pool)
	signer := testSigner(t)
	actor := makeDevice(t, ctx, pool, tenant.ID, "trivia.host")
	cookie := deviceCookie(t, ctx, pool, signer, tenant, actor)

	// Lift the raw token out of the cookie, as someone who read it off the
	// device's disk could.
	raw, ok := signer.extractToken(deviceRequest(tenant, cookie))
	if !ok {
		t.Fatal("could not extract raw token from cookie")
	}

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called = true })
	for name, h := range map[string]http.Handler{
		"mcp gate": MCPAuthGate(pool, "https://kit.example", next),
		"bearer":   BearerMiddleware(pool, next),
	} {
		req := httptest.NewRequest(http.MethodPost, "/"+tenant.Slug+"/mcp", nil)
		req.Header.Set("Authorization", "Bearer "+raw)
		req = req.WithContext(context.WithValue(req.Context(), tenantCtxKey, tenant))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized || called {
			t.Fatalf("%s: status = %d, called = %v; want 401 and not called", name, rec.Code, called)
		}
	}
	if InjectCallerFromRequest(ctx, pool, func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/x", nil)
		req.Header.Set("Authorization", "Bearer "+raw)
		return req
	}()).Value(callerKey) != nil {
		t.Fatal("InjectCallerFromRequest admitted a device token")
	}
}

func TestUserSessionUnchanged(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	tenant := oneTenant(t, ctx, pool)
	user := makeUser(t, ctx, pool, tenant.ID, "U_dev")
	signer := testSigner(t)

	rec := httptest.NewRecorder()
	if err := signer.Issue(ctx, rec, pool, tenant.ID, user.ID, "/"+tenant.Slug+"/"); err != nil {
		t.Fatal(err)
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookieName && c.Value != "" {
			cookie = c
		}
	}

	var got *services.Caller
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = CallerFromContext(r.Context()) })
	rec = httptest.NewRecorder()
	signer.Middleware(pool, next).ServeHTTP(rec, deviceRequest(tenant, cookie))
	if rec.Code != http.StatusOK || got == nil || !got.IsUser() || got.UserID != user.ID {
		t.Fatalf("status = %d, caller = %+v", rec.Code, got)
	}
}

// A device seen inside the renewal window gets its row extended and a
// fresh cookie; one with plenty of life left is left alone.
func TestDeviceSessionSlides(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	tenant := oneTenant(t, ctx, pool)
	signer := testSigner(t)
	actor := makeDevice(t, ctx, pool, tenant.ID, "trivia.host")

	raw, hash, err := opaquetoken.New(apiTokenPrefix)
	if err != nil {
		t.Fatal(err)
	}
	soon := time.Now().Add(deviceRenewWithin - time.Hour)
	if err := models.CreateAPIToken(ctx, pool, models.NewAPIToken{
		TenantID: tenant.ID, ActorID: &actor.ID, Kind: models.TokenKindDevice, TokenHash: hash, ExpiresAt: soon,
	}); err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: SessionCookieName, Value: signer.signValue(raw)}

	rec := httptest.NewRecorder()
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {})
	AllowDeviceCallers(signer.Middleware(pool, next)).ServeHTTP(rec, deviceRequest(tenant, cookie))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	renewed := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookieName && c.Value != "" && c.Path == "/"+tenant.Slug+"/" {
			renewed = true
		}
	}
	if !renewed {
		t.Fatal("expected a fresh session cookie")
	}
	var expiresAt time.Time
	if err := pool.QueryRow(ctx, `SELECT expires_at FROM api_tokens WHERE token_hash = $1`, hash).Scan(&expiresAt); err != nil {
		t.Fatal(err)
	}
	if expiresAt.Before(soon.Add(24 * time.Hour)) {
		t.Fatalf("expires_at = %v, not extended past %v", expiresAt, soon)
	}
}
