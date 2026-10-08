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
