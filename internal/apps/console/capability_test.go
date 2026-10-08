package console

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/testdb"
)

// The RequireCap wrappers run the real tenant + session chain, so these
// drive actual cookies through an actual mux: an admin and a member get
// today's answers on every kind of route, and a device gets 200 or 403 on
// exactly whether it holds the capability.

type capFixture struct {
	t      *testing.T
	ctx    context.Context
	pool   *pgxpool.Pool
	signer *auth.SessionSigner
	tenant *models.Tenant
}

func newCapFixture(t *testing.T) *capFixture {
	t.Helper()
	pool := testdb.Open(t)
	ctx := context.Background()
	teamID := "T_cap_" + uuid.NewString()
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "Cap Tenant", "enc", models.SanitizeSlug("cap-"+uuid.NewString(), teamID), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID) })
	signer, err := auth.NewSessionSigner("cap-test-secret-with-plenty-of-entropy")
	if err != nil {
		t.Fatal(err)
	}
	return &capFixture{t: t, ctx: ctx, pool: pool, signer: signer, tenant: tenant}
}

func (f *capFixture) userCookie(admin bool) *http.Cookie {
	f.t.Helper()
	user, err := models.GetOrCreateUser(f.ctx, f.pool, f.tenant.ID, "U_"+uuid.NewString()[:8], "Cap User", "")
	if err != nil {
		f.t.Fatal(err)
	}
	if admin {
		if _, err := models.GetOrCreateRole(f.ctx, f.pool, f.tenant.ID, models.RoleAdmin, ""); err != nil {
			f.t.Fatal(err)
		}
		if err := models.AssignRole(f.ctx, f.pool, f.tenant.ID, user.ID, models.RoleAdmin); err != nil {
			f.t.Fatal(err)
		}
	}
	rec := httptest.NewRecorder()
	if err := f.signer.Issue(f.ctx, rec, f.pool, f.tenant.ID, user.ID, "/"+f.tenant.Slug+"/"); err != nil {
		f.t.Fatal(err)
	}
	return sessionCookie(f.t, rec)
}

func (f *capFixture) deviceCookie(caps ...string) *http.Cookie {
	f.t.Helper()
	actor, err := models.CreateActor(f.ctx, f.pool, f.tenant.ID, models.ActorKindDevice, "Bar iPad", caps, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	if err := f.signer.IssueDevice(f.ctx, rec, f.pool, f.tenant.ID, actor.ID, actor.Label, "/"+f.tenant.Slug+"/"); err != nil {
		f.t.Fatal(err)
	}
	return sessionCookie(f.t, rec)
}

func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.SessionCookieName && c.Value != "" {
			return c
		}
	}
	t.Fatal("no session cookie")
	return nil
}

func (f *capFixture) serve(h http.Handler, method, path string, cookie *http.Cookie) int {
	f.t.Helper()
	mux := http.NewServeMux()
	mux.Handle(method+" /{slug}"+path, h)
	req := httptest.NewRequest(method, "/"+f.tenant.Slug+path, nil)
	req.Header.Set(CSRFHeader, "1")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec.Code
}

func TestRequireCapMatrix(t *testing.T) {
	f := newCapFixture(t)
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }

	memberRoute := RequireCap(f.pool, f.signer, auth.CapTriviaHost, ok)      // human default: member
	adminRoute := RequireCap(f.pool, f.signer, auth.CapMenuHappyHour, ok)    // human default: admin
	raisedRoute := RequireCapAdmin(f.pool, f.signer, auth.CapTriviaHost, ok) // member cap, admin route
	pageRoute := RequireCapPage(f.pool, f.signer, auth.CapMenuPrint, ok)     // navigated, member default
	plainRoute := JSON(f.pool, f.signer, ok)                                 // never opted in
	adminOnlyRoute := AdminJSON(f.pool, f.signer, ok)                        // never opted in

	admin := f.userCookie(true)
	member := f.userCookie(false)
	hostDevice := f.deviceCookie(auth.CapTriviaHost, auth.CapMenuPrint)
	barDevice := f.deviceCookie(auth.CapMenuHappyHour)

	cases := []struct {
		name   string
		route  http.Handler
		cookie *http.Cookie
		want   int
	}{
		{"admin on member cap", memberRoute, admin, 204},
		{"member on member cap", memberRoute, member, 204},
		{"admin on admin cap", adminRoute, admin, 204},
		{"member on admin cap", adminRoute, member, 403},
		{"admin on raised route", raisedRoute, admin, 204},
		{"member on raised route", raisedRoute, member, 403},
		{"member on page", pageRoute, member, 204},
		{"nobody on page", pageRoute, nil, 303},
		{"nobody on member cap", memberRoute, nil, 401},

		{"host device on its cap", memberRoute, hostDevice, 204},
		{"host device on raised route of its cap", raisedRoute, hostDevice, 204},
		{"host device on the print page", pageRoute, hostDevice, 204},
		{"host device on another cap", adminRoute, hostDevice, 403},
		{"bar device on happy hour", adminRoute, barDevice, 204},
		{"bar device on trivia", memberRoute, barDevice, 403},
		{"bar device on the print page", pageRoute, barDevice, 403},

		{"device on a plain route", plainRoute, hostDevice, 403},
		{"device on an admin route", adminOnlyRoute, barDevice, 403},
		{"member on a plain route", plainRoute, member, 204},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := f.serve(c.route, http.MethodPost, "/api/thing", c.cookie); got != c.want {
				t.Fatalf("status = %d, want %d", got, c.want)
			}
		})
	}
}
