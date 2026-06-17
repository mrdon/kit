// Django-style "urls.go" — maps HTTP paths to handlers for the console.
package console

import (
	"net/http"

	consoleweb "github.com/mrdon/kit/web/console"
)

func registerConsoleRoutes(mux *http.ServeMux, a *App) {
	page := func(h http.HandlerFunc) http.Handler { return PageRoute(a.pool, a.signer, h) }
	jsonRoute := func(h http.HandlerFunc) http.Handler { return JSON(a.pool, a.signer, h) }
	adminJSON := func(h http.HandlerFunc) http.Handler { return AdminJSON(a.pool, a.signer, h) }

	// Shared console bundle — one copy serves every workspace.
	mux.Handle("GET /console/assets/", consoleweb.AssetHandler())
	// The vault SharedWorker is emitted unhashed at the dist root (it
	// needs a stable URL); served here, never under /console/assets/.
	mux.Handle("GET /console/vault-worker.js", consoleweb.StaticFileHandler(
		"vault-worker.js", "application/javascript; charset=utf-8"))

	// Shell: the index and every client-side route serve the same SPA
	// HTML. The {rest...} wildcard covers /{slug}/web/ and any deeper
	// client route (e.g. /{slug}/web/integrations) on reload.
	mux.Handle("GET /{slug}/"+Segment, page(a.handleShell))
	mux.Handle("GET /{slug}/"+Segment+"/{rest...}", page(a.handleShell))

	// JSON API owned by the console itself. The API lives under
	// /{slug}/api/... (NOT under the /web shell prefix) — it's reached by
	// fetch, never navigated to, and the cards service worker already
	// skips anything containing /api/, so it's cache-safe for free.
	// Feature apps register their own /{slug}/api/... routes the same way.
	mux.Handle("GET /{slug}/api/me", jsonRoute(a.handleMe))
	mux.Handle("GET /{slug}/api/integrations", adminJSON(a.handleIntegrations))

	// Role membership matrix. Admin-only: the page reveals every user and
	// lets an admin change who is in which role.
	mux.Handle("GET /{slug}/api/roles", adminJSON(a.handleRoles))
	mux.Handle("POST /{slug}/api/roles/assign", adminJSON(a.handleRoleAssign))
	mux.Handle("POST /{slug}/api/roles/unassign", adminJSON(a.handleRoleUnassign))
}
