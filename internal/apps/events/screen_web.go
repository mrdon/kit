package events

import (
	"io"
	"log/slog"
	"net/http"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/auth"
)

// registerScreenRoutes mounts the wall screen. Public, like the menu board:
// only TenantFromPath wraps it, and the {slug} is what puts it behind the
// app-enablement gate, so turning the events app off 404s the screen too.
//
// No token, unlike the feed. The feed is a build-time API handing over
// descriptions and registration links in bulk; this is a page whose whole
// content is already on the website and ten feet tall on a wall, and a TV
// pointed at it by a kiosk board has no way to send a header.
func registerScreenRoutes(mux apps.Mux, a *App) {
	tenantMW := auth.TenantFromPath(a.pool)
	mux.Handle("GET /{slug}/events/screen", tenantMW(http.HandlerFunc(a.handleScreen)))
	// A few bytes the page polls. The screen reloads when this moves, which
	// happens on an edit and, because the date is part of it, every midnight.
	mux.Handle("GET /{slug}/events/screen.version", tenantMW(http.HandlerFunc(a.handleScreenVersion)))
}

func (a *App) handleScreen(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenant := auth.TenantFromContext(ctx)
	if tenant == nil {
		http.NotFound(w, r)
		return
	}
	s, err := a.buildScreen(ctx, tenant)
	if err != nil {
		slog.Error("events screen: building", "tenant_id", tenant.ID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Stamped before the posters are loaded: the version is about which
	// posters, not their bytes, and it has to match what the version route
	// computes without loading any.
	version := screenVersion(s, tenant.Icon192)
	page, err := renderScreen(s, version, a.screenPosters(ctx, tenant.ID, s), tenant.Icon192)
	if err != nil {
		slog.Error("events screen: rendering", "tenant_id", tenant.ID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// no-store for the menu's reason: a cached copy pins a wall to last
	// week's programme with nothing on the admin side to say why.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	if _, err := w.Write(page); err != nil {
		slog.Warn("events screen: writing response", "tenant_id", tenant.ID, "error", err)
	}
}

func (a *App) handleScreenVersion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenant := auth.TenantFromContext(ctx)
	if tenant == nil {
		http.NotFound(w, r)
		return
	}
	s, err := a.buildScreen(ctx, tenant)
	if err != nil {
		slog.Error("events screen: versioning", "tenant_id", tenant.ID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	if _, err := io.WriteString(w, screenVersion(s, tenant.Icon192)); err != nil {
		slog.Warn("events screen: writing version", "error", err)
	}
}
