package cards

import (
	"encoding/json"
	"net/http"

	"github.com/mrdon/kit/internal/auth"
)

// handleManifest returns the per-workspace PWA manifest. Uniform name
// ("Kit") across workspaces so the manifest does not leak tenant
// existence or display name to unauthenticated callers — Android still
// differentiates installs by the slug-keyed `id` / `start_url` / `scope`
// trio.
//
// Requires TenantFromPath to have resolved the slug earlier in the chain;
// if it wasn't (misrouted), returns 500.
func handleManifest(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.Error(w, "tenant not resolved", http.StatusInternalServerError)
		return
	}
	slug := tenant.Slug
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":               "/" + slug + "/",
		"name":             "Kit",
		"short_name":       "Kit",
		"start_url":        "/" + slug + "/",
		"scope":            "/" + slug + "/",
		"display":          "standalone",
		"background_color": "#0f172a",
		"theme_color":      "#1f2937",
		"icons": []map[string]any{
			{"src": "/" + slug + "/icon-192.png", "sizes": "192x192", "type": "image/png", "purpose": "any maskable"},
			{"src": "/" + slug + "/icon-512.png", "sizes": "512x512", "type": "image/png", "purpose": "any maskable"},
		},
	})
}
