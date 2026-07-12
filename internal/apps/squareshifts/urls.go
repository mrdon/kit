// Django-style urls.go — maps HTTP paths to handlers for the Square Shift
// Sync admin Manage page. Handler bodies live in web_console.go.
package squareshifts

import (
	"net/http"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/console"
)

// registerSquareShiftsRoutes wires the React console's JSON endpoints for the
// Manage page (/{slug}/web/admin/square-shifts). All admin-only, via
// console.AdminJSON (tenant + session + admin + CSRF).
func registerSquareShiftsRoutes(mux apps.Mux, a *App) {
	adminJSON := func(h http.HandlerFunc) http.Handler {
		return console.AdminJSON(a.pool, a.signer, h)
	}
	mux.Handle("GET /{slug}/api/square-shifts/status", adminJSON(a.handleStatusJSON))
	mux.Handle("POST /{slug}/api/square-shifts/sync", adminJSON(a.handleSyncJSON))
}
