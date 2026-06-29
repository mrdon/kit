// Django-style "urls.go" — the single file that maps HTTP paths to handlers
// for the vault app. Handler implementations live in web.go.
package vault

import (
	"net/http"
	"strings"
	"time"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/auth"
)

// csrfHeader is the custom request header every state-changing vault
// route requires. The session cookie is SameSite=Lax (top-level GET
// navigations carry it), so the custom header is the actual CSRF
// defense — cross-origin requests can't set a custom header without a
// preflight, and we don't allow other origins.
const csrfHeader = "X-Kit-Vault"

// registerVaultRoutes wires all /{slug}/apps/vault/... routes onto the
// mux. Each route runs through the same middleware chain as the cards
// stack:
//
//	tenantMW (resolves slug → tenant)
//	→ requireJSON or requireCSRFHeader (CSRF defense)
//	→ signer.Middleware (resolves session cookie → Caller)
//	→ AssertTenantMatch (rejects if cookie tenant ≠ path tenant)
//	→ requireCaller (refuses if no Caller landed in ctx)
//	→ handler
//
// The reveal page additionally runs the deep-link middleware between
// tenantMW and signer.Middleware so a Slack-issued one-shot token can
// mint a session without requiring an OAuth round-trip.
//
// HTML page routes (GET /vault/register etc.) skip the JSON / CSRF gate
// since they're plain navigations.
func registerVaultRoutes(mux apps.Mux, a *App) {
	if a.signer == nil {
		// Without a signer we can't authenticate anything; refuse to
		// register routes so 404 is the user-visible behaviour rather
		// than a permissive "no caller" 401 leak.
		return
	}

	tenantMW := auth.TenantFromPath(a.pool)

	// Page-style route (tenant + session, no JSON / CSRF gate), wrapped in
	// auth.PageRoute so any auth failure (missing cookie, stale cookie,
	// wrong tenant) becomes a 303 to /{slug}/login with return_to — users
	// landing on the reveal bridge from an agent-issued deep link expect
	// to "just log in", not see a bare 401. Now used only as the base for
	// the reveal bridge below.
	page := func(h http.HandlerFunc) http.Handler {
		return auth.PageRoute(tenantMW(a.signer.Middleware(a.pool, auth.AssertTenantMatch(a.signer, requireCallerHandler(h)))))
	}

	// revealPage layers the deep-link consumer ahead of the cookie
	// middleware so a fresh token mints a session before the cookie
	// check runs. Requests without `?t=` fall through untouched and
	// hit the existing cookie/OAuth path. If no deepLinks signer is
	// configured (sessionSecret missing), this collapses to the same
	// chain as page().
	revealPage := page
	if a.deepLinks != nil {
		deeplinkMW := a.deepLinks.Middleware(auth.DeepLinkMiddlewareConfig{
			Pool:       a.pool,
			Sessions:   a.signer,
			SessionTTL: time.Hour,
			BindCheck: func(r *http.Request, c *auth.Claims) bool {
				return r.PathValue("entry_id") == c.EntryID.String()
			},
			OnError:   a.handleDeepLinkError,
			OnSuccess: a.handleDeepLinkSuccess,
		})
		revealPage = func(h http.HandlerFunc) http.Handler {
			return auth.PageRoute(tenantMW(deeplinkMW(a.signer.Middleware(a.pool, auth.AssertTenantMatch(a.signer, requireCallerHandler(h))))))
		}
	}

	// JSON state-changing API: tenant + JSON content-type + session.
	wrap := func(h http.HandlerFunc) http.Handler {
		return tenantMW(requireJSON(a.signer.Middleware(a.pool, auth.AssertTenantMatch(a.signer, requireCallerHandler(h)))))
	}

	// JSON GET API: tenant + session, no JSON gate (GETs have no body).
	get := func(h http.HandlerFunc) http.Handler {
		return tenantMW(a.signer.Middleware(a.pool, auth.AssertTenantMatch(a.signer, requireCallerHandler(h))))
	}

	// The vault UI lives in the React console (/{slug}/web/vault); only
	// the JSON API the console posts to lives here. It sits under the
	// shared /{slug}/api/... namespace like every other feature app's
	// API (task, expense, netlify, widget) — NOT under /apps/. The one
	// exception is the reveal bridge below, which is a redirect, not API.

	// Admin-only setup, rotate, nuke (browser-driven crypto).
	mux.Handle("POST /{slug}/api/vault/setup", wrap(a.handleSetupPost))
	mux.Handle("POST /{slug}/api/vault/rotate", wrap(a.handleRotatePost))
	mux.Handle("POST /{slug}/api/vault/nuke", wrap(a.handleNukePost))

	// Unlock / lock / status
	mux.Handle("POST /{slug}/api/vault/unlock", wrap(a.handleUnlock))
	mux.Handle("POST /{slug}/api/vault/lock", wrap(a.handleLock))
	mux.Handle("GET /{slug}/api/vault/status", get(a.handleStatus))

	// Principal listing — populates the "who can see this" selector
	// in the React add / reveal panels.
	mux.Handle("GET /{slug}/api/vault/principals", get(a.handlePrincipals))

	// Reveal bridge — the deep-link-aware wrapper lets a Slack-issued
	// one-shot token mint a session without an OAuth round-trip, then
	// the handler bounces into the React vault entry. This is the only
	// route still under /apps/vault; it renders no HTML of its own.
	mux.Handle("GET /{slug}/apps/vault/reveal/{entry_id}", revealPage(a.handleRevealPage))

	// Entries CRUD (browser-driven; ciphertext on the wire)
	mux.Handle("GET /{slug}/api/vault/entries", get(a.handleListEntries))
	mux.Handle("POST /{slug}/api/vault/entries", wrap(a.handleCreateEntry))
	mux.Handle("GET /{slug}/api/vault/entries/{entry_id}", get(a.handleGetEntry))
	mux.Handle("PUT /{slug}/api/vault/entries/{entry_id}", wrap(a.handleUpdateEntry))
	mux.Handle("PUT /{slug}/api/vault/entries/{entry_id}/role", wrap(a.handleSetEntryRole))
	mux.Handle("DELETE /{slug}/api/vault/entries/{entry_id}", wrap(a.handleDeleteEntry))
}

// requireJSON rejects state-changing requests that lack BOTH the
// X-Kit-Vault header (custom-header CSRF defense) AND, for requests with
// a body, application/json Content-Type. The combination guards against:
//   - Cross-origin form POSTs (browsers force form-encoded, which fails
//     the JSON check)
//   - Cross-origin <img>/<script> GET-with-side-effects (none of our
//     routes use GET to mutate)
//   - Anything that can't set custom headers without a CORS preflight
//     (custom headers are forbidden by simple-request rules; preflight
//     fails because we don't allow other origins)
func requireJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodDelete:
			if r.Header.Get(csrfHeader) != "1" {
				http.Error(w, "missing "+csrfHeader+" header", http.StatusUnsupportedMediaType)
				return
			}
			// Bodyless DELETE doesn't need a content type; everything
			// else must be JSON.
			if r.Method != http.MethodDelete || r.ContentLength != 0 {
				ct := r.Header.Get("Content-Type")
				if !strings.HasPrefix(ct, "application/json") {
					http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// requireCallerHandler refuses requests where the upstream session
// middleware didn't land a Caller in ctx. Defence against ordering bugs
// in the chain.
func requireCallerHandler(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth.CallerFromContext(r.Context()) == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h(w, r)
	})
}
