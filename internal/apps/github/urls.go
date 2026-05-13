// Django-style "urls.go" — single file mapping HTTP paths to handlers
// for the github (App-install substrate) Kit app.
package github

import (
	"net/http"

	"github.com/mrdon/kit/internal/auth"
)

// registerGitHubRoutes wires per-tenant connect/disconnect routes plus
// the tenant-agnostic top-level install callback. v1 has no settings
// surface of its own — the install button is rendered by whichever
// app (today: netlify) needs GitHub access; it links here with
// `return_to=<that-app's-settings-url>`.
func registerGitHubRoutes(mux *http.ServeMux, a *App) {
	page := func(h http.HandlerFunc) http.Handler {
		tenantMW := auth.TenantFromPath(a.pool)
		return auth.PageRoute(tenantMW(a.signer.Middleware(a.pool,
			auth.AssertTenantMatch(a.signer, requireAdminHandler(h)))))
	}

	callback := func(h http.HandlerFunc) http.Handler {
		return auth.PageRoute(a.signer.Middleware(a.pool, requireAdminHandler(h)))
	}

	mux.Handle("GET /{slug}"+installPathPrefix+"/connect", page(a.handleConnect))
	mux.Handle("POST /{slug}"+installPathPrefix+"/disconnect", page(a.handleDisconnect))
	mux.Handle("GET "+callbackPath, callback(a.handleCallback))
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
