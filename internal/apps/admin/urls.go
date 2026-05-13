// Django-style "urls.go" — single file mapping HTTP paths to handlers
// for the admin dashboard app.
package admin

import (
	"net/http"

	"github.com/mrdon/kit/internal/auth"
)

func registerAdminRoutes(mux *http.ServeMux, a *App) {
	tenantMW := auth.TenantFromPath(a.pool)
	page := func(h http.HandlerFunc) http.Handler {
		return auth.PageRoute(tenantMW(a.signer.Middleware(a.pool,
			auth.AssertTenantMatch(a.signer, requireAdminHandler(h)))))
	}

	mux.Handle("GET /{slug}/admin/integrations", page(a.handleIntegrationsIndex))
}

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
