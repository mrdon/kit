package console

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/auth"
)

// CSRFHeader is the custom header state-changing console requests must
// carry. One header for every browser surface; the rule itself lives in
// auth.RequireCSRF.
const CSRFHeader = auth.CSRFHeader

// PageRoute wraps a full-page console HTML route. auth.PageRoute marks it
// as an HTML navigation so a missing/stale session 303-redirects to
// /{slug}/login instead of returning a bare 401.
func PageRoute(pool *pgxpool.Pool, signer *auth.SessionSigner, h http.HandlerFunc) http.Handler {
	tenantMW := auth.TenantFromPath(pool)
	return auth.PageRoute(tenantMW(signer.Middleware(pool,
		auth.AssertTenantMatch(signer, requireCallerHandler(h)))))
}

// ShellRoute is PageRoute for the React shell itself: a paired device
// must be able to load the HTML (and, via MeRoute, learn what it is) so
// the shell can render the device's screens. Nothing else is granted
// here; every API the shell then calls is refused unless it opted in.
func ShellRoute(pool *pgxpool.Pool, signer *auth.SessionSigner, h http.HandlerFunc) http.Handler {
	tenantMW := auth.TenantFromPath(pool)
	return auth.PageRoute(tenantMW(auth.AllowDeviceCallers(signer.Middleware(pool,
		auth.AssertTenantMatch(signer, requireCallerHandler(h))))))
}

// MeRoute is JSON for /api/me, which any caller kind may read.
func MeRoute(pool *pgxpool.Pool, signer *auth.SessionSigner, h http.HandlerFunc) http.Handler {
	tenantMW := auth.TenantFromPath(pool)
	return tenantMW(auth.AllowDeviceCallers(signer.Middleware(pool,
		auth.AssertTenantMatch(signer, auth.RequireCSRF(requireCallerHandler(h))))))
}

// JSON wraps a console JSON API route. It is NOT a PageRoute, so a missing
// session yields 401 (not a 303-to-login that would dump login HTML into
// fetch().json()). State-changing methods must carry the X-Kit-Web header.
// Exported so feature apps can register their own /{slug}/web/api/... routes
// with the identical auth + CSRF contract.
func JSON(pool *pgxpool.Pool, signer *auth.SessionSigner, h http.HandlerFunc) http.Handler {
	tenantMW := auth.TenantFromPath(pool)
	return tenantMW(signer.Middleware(pool,
		auth.AssertTenantMatch(signer, auth.RequireCSRF(requireCallerHandler(h)))))
}

// AdminJSON is JSON plus an IsAdmin gate. Security lives here on the API;
// clients only hide admin nav cosmetically.
func AdminJSON(pool *pgxpool.Pool, signer *auth.SessionSigner, h http.HandlerFunc) http.Handler {
	tenantMW := auth.TenantFromPath(pool)
	return tenantMW(signer.Middleware(pool,
		auth.AssertTenantMatch(signer, auth.RequireCSRF(requireAdminHandler(h)))))
}

// requireCallerHandler runs h only if the session middleware left a caller
// in the context.
func requireCallerHandler(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth.CallerFromContext(r.Context()) == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h(w, r)
	})
}

// requireAdminHandler runs h only for admin callers.
func requireAdminHandler(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller := auth.CallerFromContext(r.Context())
		if caller == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if !caller.IsAdmin {
			http.Error(w, "admin only", http.StatusForbidden)
			return
		}
		h(w, r)
	})
}

// RequireCap guards a JSON route with a capability. A person passes when
// their role meets the capability's human default (so nothing changes for
// people); a paired device passes when it holds the capability. Devices
// are admitted past the session middleware only here and on the shell, so
// a route that doesn't use RequireCap never sees one.
func RequireCap(pool *pgxpool.Pool, signer *auth.SessionSigner, capName string, h http.HandlerFunc) http.Handler {
	c := mustCapability(capName)
	return capRoute(pool, signer, false, requireCapHandler(c, c.Human, h))
}

// RequireCapAdmin is RequireCap with the human check raised to admin for
// this route, for the few routes inside a member-level capability that
// are workspace settings rather than operational work.
func RequireCapAdmin(pool *pgxpool.Pool, signer *auth.SessionSigner, capName string, h http.HandlerFunc) http.Handler {
	c := mustCapability(capName)
	return capRoute(pool, signer, false, requireCapHandler(c, auth.HumanAdmin, h))
}

// RequireCapPage is RequireCap for a navigated page (a PDF, not a fetch):
// no CSRF gate, and an auth failure redirects to login.
func RequireCapPage(pool *pgxpool.Pool, signer *auth.SessionSigner, capName string, h http.HandlerFunc) http.Handler {
	c := mustCapability(capName)
	return capRoute(pool, signer, true, requireCapHandler(c, c.Human, h))
}

func capRoute(pool *pgxpool.Pool, signer *auth.SessionSigner, page bool, h http.Handler) http.Handler {
	tenantMW := auth.TenantFromPath(pool)
	if page {
		return auth.PageRoute(tenantMW(auth.AllowDeviceCallers(signer.Middleware(pool,
			auth.AssertTenantMatch(signer, h)))))
	}
	return tenantMW(auth.AllowDeviceCallers(signer.Middleware(pool,
		auth.AssertTenantMatch(signer, auth.RequireCSRF(h)))))
}

func mustCapability(name string) auth.Capability {
	c, ok := auth.LookupCapability(name)
	if !ok {
		panic("console: unknown capability " + name)
	}
	return c
}

func requireCapHandler(c auth.Capability, human auth.HumanCheck, h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller := auth.CallerFromContext(r.Context())
		if caller == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if !c.Allows(caller, human) {
			if caller.IsUser() {
				http.Error(w, "admin only", http.StatusForbidden)
			} else {
				http.Error(w, "this device can't "+c.Label, http.StatusForbidden)
			}
			return
		}
		h(w, r)
	})
}
