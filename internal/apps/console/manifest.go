package console

import (
	"encoding/json"
	"net/http"

	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/services"
)

// handleManifest is the console's own PWA manifest, so it installs as an app
// distinct from the cards PWA (its own id) and opens on the console.
//
// A paired device starts at /{slug}/pair rather than the console. A
// home-screen app on an iPad does not reliably share Safari's cookies, so the
// installed copy may launch unpaired, and the console would send it to Slack
// login with no address bar to get anywhere else. /pair sends a paired device
// straight on to the console and pairs one that isn't, so it is right either
// way. The manifest is fetched without cookies, which is why the shell picks
// this for the device with ?start=pair instead of the handler reading the
// session.
func handleManifest(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.Error(w, "tenant not resolved", http.StatusInternalServerError)
		return
	}
	base := "/" + tenant.Slug + "/"
	name := tenant.Name
	if name == "" {
		name = tenant.Slug
	}
	start := base + Segment + "/"
	if r.URL.Query().Get("start") == "pair" {
		start = base + "pair"
	}
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":         base + Segment + "/",
		"name":       name,
		"short_name": name,
		"start_url":  start,
		// The whole workspace, so /pair's redirect into the console stays
		// inside the app instead of opening a browser sheet.
		"scope":            base,
		"display":          "standalone",
		"background_color": "#0f172a",
		"theme_color":      "#1f2937",
		"icons": []map[string]any{
			{"src": base + "icon-192.png", "sizes": "192x192", "type": "image/png", "purpose": "any"},
			{"src": base + "icon-512.png", "sizes": "512x512", "type": "image/png", "purpose": "any"},
		},
	})
}

// manifestURL is the manifest the shell links for this caller.
func manifestURL(slug string, caller *services.Caller) string {
	u := "/" + slug + "/" + Segment + "/manifest.webmanifest"
	if caller != nil && caller.Kind == services.CallerDevice {
		u += "?start=pair"
	}
	return u
}
