// Django-style "urls.go" — single file mapping HTTP paths to handlers
// for the admin dashboard app.
package admin

import (
	"net/http"

	"github.com/mrdon/kit/internal/auth"
)

func registerAdminRoutes(mux *http.ServeMux, a *App) {
	tenantMW := auth.TenantFromPath(a.pool)

	// The integrations index moved into the React console at
	// /{slug}/web/admin/integrations. Keep the old admin URL working — it's
	// referenced from Slack DMs, agent messages, and bookmarks — by
	// 302-redirecting to the console route. The literal "web" segment
	// matches console.Segment; admin can't import console (console imports
	// admin's Integration registry, which would cycle).
	mux.Handle("GET /{slug}/admin/integrations", tenantMW(http.HandlerFunc(redirectToConsoleIntegrations)))
}

func redirectToConsoleIntegrations(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.Error(w, "tenant not resolved", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/"+tenant.Slug+"/web/admin/integrations", http.StatusFound)
}
