// Django-style "urls.go" — the single file that maps HTTP paths to handlers
// for the Netlify app. Handler implementations live in web.go and oauth.go.
package netlify

import (
	"net/http"

	"github.com/mrdon/kit/internal/auth"
)

// registerNetlifyRoutes wires both the per-tenant settings routes and
// the tenant-agnostic OAuth callback paths onto the mux.
//
// Per-tenant page chain mirrors vault:
//
//	tenantMW (resolves slug → tenant)
//	→ signer.Middleware (resolves session cookie → Caller)
//	→ AssertTenantMatch (rejects if cookie tenant ≠ path tenant)
//	→ requireAdmin
//
// OAuth callbacks live at tenant-agnostic top-level paths
// (`/oauth/netlify/callback`, `/oauth/github/callback`) so a single
// fixed redirect URI can be registered with each upstream provider.
// The callback handler identifies the tenant via the state cookie's
// embedded slug and verifies the session cookie's caller matches.
// Skip tenantMW + AssertTenantMatch here — the handler does its own
// tenant resolution.
//
// All routes are admin-only in v1 — the technical operator is the only
// one who configures the connection. Once role-gated requesting lands
// (v2), individual change requests may open up to a wider role; the
// settings page stays admin-only.
func registerNetlifyRoutes(mux *http.ServeMux, a *App) {
	tenantMW := auth.TenantFromPath(a.pool)

	page := func(h http.HandlerFunc) http.Handler {
		return auth.PageRoute(tenantMW(a.signer.Middleware(a.pool,
			auth.AssertTenantMatch(a.signer, requireAdminHandler(h)))))
	}

	// Settings landing page.
	mux.Handle("GET /{slug}/apps/netlify/settings", page(a.handleSettingsPage))

	// Netlify OAuth dance. Top-level callback is a tiny bouncer that
	// reads the slug from the state cookie (Path=/) and 303s to the
	// per-tenant callback URL where the session cookie (Path=/{slug}/)
	// is in scope. See handleNetlifyCallbackBounce for the rationale.
	mux.Handle("GET /{slug}/apps/netlify/connect/netlify", page(a.handleNetlifyConnect))
	mux.HandleFunc("GET /oauth/netlify/callback", a.handleNetlifyCallbackBounce)
	mux.Handle("GET /{slug}/oauth/netlify/callback", page(a.handleNetlifyCallback))
	mux.Handle("POST /{slug}/apps/netlify/site", page(a.handleNetlifySitePick))
	mux.Handle("POST /{slug}/apps/netlify/disconnect/netlify", page(a.handleNetlifyDisconnect))

	// GitHub install lives in the github Kit app (shared substrate,
	// one install per tenant). The "Install GitHub App" + Disconnect
	// buttons on this app's settings page link to the github app's
	// routes with return_to=/{slug}/apps/netlify/settings.
}

// requireAdminHandler refuses requests where the caller is not a
// tenant admin. The settings page is configuration, not day-to-day
// use — only admins should touch it.
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
