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
	// The workspace's menu lives at /{slug}/menu with no key. That address
	// exists from the moment the app is on, before anyone has set a tap list,
	// so it can be pasted onto a screen once and never revisited.
	mux.Handle("GET /{slug}/menu", tenantMW(http.HandlerFunc(a.handleBoard)))
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
	if key == "" {
		key = DefaultKey
	}
	row, err := a.svc.Get(r.Context(), tenant.ID, key)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// The workspace's menu always answers, even before anyone has set
			// a tap list -- a 404 here would mean the address could not be
			// wired to a screen until content existed, which is backwards. An
			// explicitly-keyed board that does not exist is a real 404.
			if key == DefaultKey {
				writePlaceholder(w)
				return
			}
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

// writePlaceholder serves the menu before anyone has set a tap list. A 200
// with a readable page beats a 404: the screen is working and correctly
// addressed, it has simply been given nothing to show, and someone walking
// past should be able to tell those apart from across the room.
//
// It refreshes itself so a screen already hanging on the wall picks up the
// first real tap list without anyone rebooting it.
func writePlaceholder(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(placeholderHTML)); err != nil {
		slog.Warn("writing menu placeholder", "error", err)
	}
}

const placeholderHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="60">
<title>Menu</title>
<style>
  html, body { height: 100%; margin: 0; }
  body {
    display: flex; flex-direction: column;
    align-items: center; justify-content: center;
    background: #14222d; color: #b9c4cf;
    font: 400 1rem/1.5 system-ui, -apple-system, "Segoe UI", sans-serif;
    text-align: center; padding: 2rem;
  }
  h1 { font-size: clamp(1.5rem, 5vw, 3rem); margin: 0 0 .75rem; color: #fff; }
  p { margin: 0; color: #8ea2b3; font-size: clamp(.9rem, 2vw, 1.25rem); }
</style>
</head>
<body>
  <h1>Nothing on tap yet</h1>
  <p>This screen is working. Set the tap list and it will appear here.</p>
</body>
</html>
`
