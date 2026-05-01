// Django-style "urls.go" — the single file that maps HTTP paths to handlers
// for the vault app. Handler implementations live in web.go.
package vault

import (
	"errors"
	"net/http"
	"strings"

	"github.com/mrdon/kit/internal/auth"
)

// csrfHeader is the custom request header every state-changing vault
// route requires. SameSite=Strict on the session cookie blocks most
// cross-origin attacks; the header is belt-and-suspenders against any
// flaw that lets a CORS preflight slip through.
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
// HTML page routes (GET /vault/register etc.) skip the JSON / CSRF gate
// since they're plain navigations.
func registerVaultRoutes(mux *http.ServeMux, a *App) {
	if a.signer == nil {
		// Without a signer we can't authenticate anything; refuse to
		// register routes so 404 is the user-visible behaviour rather
		// than a permissive "no caller" 401 leak.
		return
	}

	tenantMW := auth.TenantFromPath(a.pool)

	// HTML pages: tenant + session, but no JSON / CSRF gate. If the
	// session cookie is missing entirely, redirect to /{slug}/login
	// (PWA's Slack-OpenID kickoff) instead of returning a bare 401 —
	// the user landed here from an agent link and expects to "just log
	// in". Tampered/invalid cookies still 401 via signer.Middleware so
	// we don't paper over auth bugs.
	page := func(h http.HandlerFunc) http.Handler {
		inner := tenantMW(a.signer.Middleware(a.pool, auth.AssertTenantMatch(a.signer, requireCallerHandler(h))))
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := r.Cookie(auth.SessionCookieName); errors.Is(err, http.ErrNoCookie) {
				slug := r.PathValue("slug")
				if slug == "" {
					http.Error(w, "tenant not resolved", http.StatusBadRequest)
					return
				}
				http.Redirect(w, r, "/"+slug+"/login", http.StatusSeeOther)
				return
			}
			inner.ServeHTTP(w, r)
		})
	}

	// JSON state-changing API: tenant + JSON content-type + session.
	wrap := func(h http.HandlerFunc) http.Handler {
		return tenantMW(requireJSON(a.signer.Middleware(a.pool, auth.AssertTenantMatch(a.signer, requireCallerHandler(h)))))
	}

	// JSON GET API: tenant + session, no JSON gate (GETs have no body).
	get := func(h http.HandlerFunc) http.Handler {
		return tenantMW(a.signer.Middleware(a.pool, auth.AssertTenantMatch(a.signer, requireCallerHandler(h))))
	}

	// Static asset GET: tenant + session (so we don't serve to anonymous
	// browsers; refusing to leak our app shell unauthenticated).
	static := func(h http.HandlerFunc) http.Handler {
		return tenantMW(a.signer.Middleware(a.pool, auth.AssertTenantMatch(a.signer, requireCallerHandler(h))))
	}

	// Register / unlock / lock
	mux.Handle("GET /{slug}/apps/vault/register", page(a.handleRegisterPage))
	mux.Handle("POST /{slug}/apps/vault/api/register", wrap(a.handleRegisterPost))
	mux.Handle("POST /{slug}/apps/vault/api/self_unlock_test", wrap(a.handleSelfUnlockTest))
	mux.Handle("POST /{slug}/apps/vault/api/unlock", wrap(a.handleUnlock))
	mux.Handle("POST /{slug}/apps/vault/lock", wrap(a.handleLock))
	// Reset cancel — wipes a row in 24h cooldown before a teammate can
	// re-grant. Page renders confirmation; POST does the wipe.
	mux.Handle("GET /{slug}/apps/vault/cancel_reset", page(a.handleCancelResetPage))
	mux.Handle("POST /{slug}/apps/vault/api/cancel_reset", wrap(a.handleCancelReset))
	mux.Handle("GET /{slug}/apps/vault/api/me", get(a.handleMe))
	mux.Handle("GET /{slug}/apps/vault/api/users/{user_id}", get(a.handleGetUser))
	// Principal listing — populates the "who can see this" selector
	// on the add / reveal pages.
	mux.Handle("GET /{slug}/apps/vault/api/principals", get(a.handlePrincipals))

	// Capture
	mux.Handle("GET /{slug}/apps/vault/add", page(a.handleAddPage))

	// Reveal
	mux.Handle("GET /{slug}/apps/vault/reveal/{entry_id}", page(a.handleRevealPage))

	// Entries CRUD (browser-driven; ciphertext on the wire)
	mux.Handle("GET /{slug}/apps/vault/api/entries", get(a.handleListEntries))
	mux.Handle("POST /{slug}/apps/vault/api/entries", wrap(a.handleCreateEntry))
	mux.Handle("GET /{slug}/apps/vault/api/entries/{entry_id}", get(a.handleGetEntry))
	mux.Handle("PUT /{slug}/apps/vault/api/entries/{entry_id}", wrap(a.handleUpdateEntry))
	mux.Handle("PUT /{slug}/apps/vault/api/entries/{entry_id}/role", wrap(a.handleSetEntryRole))
	mux.Handle("DELETE /{slug}/apps/vault/api/entries/{entry_id}", wrap(a.handleDeleteEntry))

	// Grants
	mux.Handle("GET /{slug}/apps/vault/grant/{user_id}", page(a.handleGrantPage))
	mux.Handle("POST /{slug}/apps/vault/api/grants/{user_id}", wrap(a.handleGrant))
	mux.Handle("DELETE /{slug}/apps/vault/api/grants/{user_id}", wrap(a.handleRevokeGrant))
	// Decline a pending registration: deletes the vault_users row entirely
	// (only valid while wrapped_vault_key IS NULL).
	mux.Handle("DELETE /{slug}/apps/vault/api/users/{user_id}", wrap(a.handleDeclinePending))

	// Static
	mux.Handle("GET /{slug}/apps/vault/static/", static(a.handleStatic))
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
