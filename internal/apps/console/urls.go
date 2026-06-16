// Django-style "urls.go" — maps HTTP paths to handlers for the console.
package console

import (
	"net/http"

	"github.com/mrdon/kit/internal/auth"
	consoleweb "github.com/mrdon/kit/web/console"
)

// csrfHeader is the custom header state-changing console requests must
// carry. Requiring a custom header lifts the request out of the CORS
// "simple request" category, which is the CSRF guard — the same pattern
// as the cards app's X-Kit-Chat and vault's X-Kit-Vault.
const csrfHeader = "X-Kit-Web"

func registerConsoleRoutes(mux *http.ServeMux, a *App) {
	tenantMW := auth.TenantFromPath(a.pool)

	// page wraps a full-page HTML route. auth.PageRoute marks it as an
	// HTML navigation so a missing/stale session 303-redirects to
	// /{slug}/login instead of returning a bare 401.
	page := func(h http.HandlerFunc) http.Handler {
		return auth.PageRoute(tenantMW(a.signer.Middleware(a.pool,
			auth.AssertTenantMatch(a.signer, requireCallerHandler(h)))))
	}

	// jsonRoute wraps a JSON API route. It is NOT a PageRoute, so a
	// missing session yields 401 (not a 303-to-login that would dump
	// login HTML into fetch().json()). State-changing methods must carry
	// the X-Kit-Web header.
	jsonRoute := func(h http.HandlerFunc) http.Handler {
		return tenantMW(a.signer.Middleware(a.pool,
			auth.AssertTenantMatch(a.signer, requireCSRF(requireCallerHandler(h)))))
	}

	// adminJSON is jsonRoute plus an IsAdmin gate. Security lives here on
	// the API; the client only hides admin nav cosmetically.
	adminJSON := func(h http.HandlerFunc) http.Handler {
		return tenantMW(a.signer.Middleware(a.pool,
			auth.AssertTenantMatch(a.signer, requireCSRF(requireAdminHandler(h)))))
	}

	// Shared console bundle — one copy serves every workspace.
	mux.Handle("GET /console/assets/", consoleweb.AssetHandler())

	// Shell: the index and every client-side route serve the same SPA
	// HTML. The {rest...} wildcard covers /{slug}/web/ and any deeper
	// client route (e.g. /{slug}/web/integrations) on reload. More
	// specific /api/... patterns below outrank it.
	mux.Handle("GET /{slug}/"+Segment, page(a.handleShell))
	mux.Handle("GET /{slug}/"+Segment+"/{rest...}", page(a.handleShell))

	// JSON API.
	mux.Handle("GET /{slug}/"+Segment+"/api/me", jsonRoute(a.handleMe))
	mux.Handle("GET /{slug}/"+Segment+"/api/integrations", adminJSON(a.handleIntegrations))
}

// requireCSRF enforces the X-Kit-Web: 1 header on state-changing methods.
// GET/HEAD pass through.
func requireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete:
			if r.Header.Get(csrfHeader) != "1" {
				http.Error(w, "missing "+csrfHeader+" header", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// requireCallerHandler runs h only if the session middleware left a
// caller in the context.
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
