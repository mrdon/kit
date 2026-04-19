package cards

import (
	"encoding/json"
	"net/http"

	"github.com/mrdon/kit/internal/auth"
)

// handleManifest returns the per-workspace PWA manifest. Each install
// uses the workspace's Slack name as its label so multiple installed
// PWAs are distinguishable on the home screen. The tradeoff: the
// manifest now leaks the tenant display name to anyone who can guess or
// discover the slug. The slug is already public (it's in the URL) and
// the icon endpoint serves the Slack team icon, so an adversary who
// enumerates slugs already learns about as much from the icon as from
// the name — the incremental leak is small relative to the UX win.
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
	displayName := tenant.Name
	if displayName == "" {
		displayName = slug
	}
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":               "/" + slug + "/",
		"name":             displayName,
		"short_name":       displayName,
		"start_url":        "/" + slug + "/",
		"scope":            "/" + slug + "/",
		"display":          "standalone",
		"background_color": "#0f172a",
		"theme_color":      "#1f2937",
		"icons": []map[string]any{
			// Icons are upscaled at install time from Slack's source
			// (max 230x230 from team.info) so the declared sizes are
			// honest. Dropping "maskable" because Slack icons have no
			// safe-zone padding and Android would crop them oddly.
			{"src": "/" + slug + "/icon-192.png", "sizes": "192x192", "type": "image/png", "purpose": "any"},
			{"src": "/" + slug + "/icon-512.png", "sizes": "512x512", "type": "image/png", "purpose": "any"},
		},
	})
}
