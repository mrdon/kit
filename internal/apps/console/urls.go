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
}
