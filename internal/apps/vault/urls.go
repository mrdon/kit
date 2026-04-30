// Django-style "urls.go" — the single file that maps HTTP paths to handlers
// for the vault app. Handler implementations live in web.go.
package vault

import (
	"net/http"

	"github.com/mrdon/kit/internal/auth"
)

// registerVaultRoutes wires all /{slug}/apps/vault/... routes onto the mux,
// each gated by the session-cookie middleware (Slack OAuth-backed).
func registerVaultRoutes(mux *http.ServeMux, a *App) {
	// All routes require an authenticated caller resolved from the
	// session cookie + the tenant slug from the path. The session
	// middleware is provided to RegisterRoutes wiring at startup; for
	// now we use TenantFromPath + a thin auth-required wrapper that
	// delegates session resolution to the existing helpers.
	tenantMW := auth.TenantFromPath(a.pool)
	authed := func(h http.HandlerFunc) http.Handler {
		return tenantMW(authRequired(http.HandlerFunc(h)))
	}

	// Register / unlock / lock
	mux.Handle("GET /{slug}/apps/vault/register", authed(a.handleRegisterPage))
	mux.Handle("POST /{slug}/apps/vault/api/register", authed(a.handleRegisterPost))
	mux.Handle("POST /{slug}/apps/vault/api/self_unlock_test", authed(a.handleSelfUnlockTest))
	mux.Handle("POST /{slug}/apps/vault/api/unlock", authed(a.handleUnlock))
	mux.Handle("POST /{slug}/apps/vault/lock", authed(a.handleLock))

	// Capture
	mux.Handle("GET /{slug}/apps/vault/add", authed(a.handleAddPage))

	// Reveal
	mux.Handle("GET /{slug}/apps/vault/reveal/{entry_id}", authed(a.handleRevealPage))

	// Entries CRUD (browser-driven; ciphertext on the wire)
	mux.Handle("GET /{slug}/apps/vault/api/entries", authed(a.handleListEntries))
	mux.Handle("POST /{slug}/apps/vault/api/entries", authed(a.handleCreateEntry))
	mux.Handle("GET /{slug}/apps/vault/api/entries/{entry_id}", authed(a.handleGetEntry))
	mux.Handle("PUT /{slug}/apps/vault/api/entries/{entry_id}", authed(a.handleUpdateEntry))
	mux.Handle("DELETE /{slug}/apps/vault/api/entries/{entry_id}", authed(a.handleDeleteEntry))

	// Grants
	mux.Handle("GET /{slug}/apps/vault/grant/{user_id}", authed(a.handleGrantPage))
	mux.Handle("POST /{slug}/apps/vault/api/grants/{user_id}", authed(a.handleGrant))
	mux.Handle("DELETE /{slug}/apps/vault/api/grants/{user_id}", authed(a.handleRevokeGrant))

	// Static + the SharedWorker JS module
	mux.Handle("GET /{slug}/apps/vault/static/", authed(a.handleStatic))
}

// authRequired enforces that a Caller has been injected by an upstream
// session/bearer middleware. Used to keep handlers from accidentally
// running unauthenticated. This is a stop-gap — wired later when the
// session signer is in scope at startup; for v1 it's a no-op pass-through
// and the handler-level auth.CallerFromContext check is the single point
// of enforcement.
func authRequired(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth.CallerFromContext(r.Context()) == nil {
			// Bearer-only path will set this; session middleware is
			// applied at the mux layer in cmd/kit/main.go (separate
			// concern). If neither has run, refuse.
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
