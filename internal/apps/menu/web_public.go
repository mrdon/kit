package menu

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/auth"
)

// registerPublicRoutes mounts the one unauthenticated route this app exists
// for. It carries {slug}, so the enablement gate in apps.RegisterAllRoutes
// 404s it automatically for a workspace that has turned the menu app off.
//
// Only TenantFromPath wraps it: no session middleware, no CSRF header, no
// caller. The screen showing this page has no credentials to present, and the
// content is a list of beers already painted ten feet tall on a wall.
func registerPublicRoutes(mux apps.Mux, a *App) {
	tenantMW := auth.TenantFromPath(a.pool)
	mux.Handle("GET /{slug}/menu/{key}", tenantMW(http.HandlerFunc(a.handleBoard)))
}

// handleBoard renders a menu board for a screen.
//
// Cache-Control: no-store is load-bearing rather than hygiene. The whole point
// of this page is that it changes — a keg blows, a price moves — and an
// intermediary that caches it pins a wall display to a tap list that is no
// longer true, with nothing on the admin side to show why.
func (a *App) handleBoard(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.NotFound(w, r)
		return
	}
	key := r.PathValue("key")
	row, err := a.svc.Get(r.Context(), tenant.ID, key)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("loading menu board", "tenant_id", tenant.ID, "key", key, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	board, err := ParseBoard(row.Payload)
	if err != nil {
		// A stored board that no longer parses means the schema moved under a
		// payload that was valid when it was pushed. Log loudly and 500: a
		// half-rendered tap list on a wall is worse than a blank screen,
		// because staff will pour from it.
		slog.Error("stored menu board failed to parse",
			"tenant_id", tenant.ID, "key", key, "error", err)
		http.Error(w, "menu board is not renderable", http.StatusInternalServerError)
		return
	}

	html, err := Render(board)
	if err != nil {
		slog.Error("rendering menu board", "tenant_id", tenant.ID, "key", key, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(html)); err != nil {
		slog.Warn("writing menu board", "tenant_id", tenant.ID, "key", key, "error", err)
	}
}
