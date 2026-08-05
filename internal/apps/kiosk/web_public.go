package kiosk

import (
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/auth"
)

// registerPublicRoutes mounts the one unauthenticated route this app exists
// for. It carries {slug}, so the enablement gate in apps.RegisterAllRoutes
// 404s it automatically for a workspace that has turned the kiosk app off —
// the app doesn't have to re-check enablement the way the widget does for its
// slug-less routes.
//
// Only TenantFromPath wraps it: no session middleware, no CSRF header, no
// caller. A kiosk has no credentials to present.
func registerPublicRoutes(mux apps.Mux, a *App) {
	tenantMW := auth.TenantFromPath(a.pool)
	mux.Handle("GET /{slug}/kiosk/{key}", tenantMW(http.HandlerFunc(a.handleBoardRedirect)))
}

// handleBoardRedirect answers a board's public URL. Two audiences, one
// response:
//
//   - A browser (the kiosk on boot, or an admin checking the link) follows the
//     302 and lands on the content.
//   - The poller running on the kiosk asks with redirects disabled and reads
//     the Location header, comparing it to what the screen currently shows.
//
// Cache-Control: no-store is load-bearing, not hygiene. This is a redirect
// whose entire purpose is to change, and an intermediary that caches it — or a
// browser that treats it as permanent — pins a screen to stale content with no
// way to tell from the admin side. For the same reason the status is 302 and
// never 301: a 301 is cached indefinitely by default and would burn the board
// key permanently on the first machine that saw it.
func (a *App) handleBoardRedirect(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.NotFound(w, r)
		return
	}
	key := r.PathValue("key")
	board, err := a.svc.Resolve(r.Context(), tenant.ID, key)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("resolving kiosk board", "tenant_id", tenant.ID, "key", key, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Best-effort heartbeat. A screen that is polling but whose touch fails
	// must still get its redirect.
	if err := TouchBoardSeen(r.Context(), a.pool, tenant.ID, board.ID); err != nil {
		slog.Warn("touching kiosk board", "board_id", board.ID, "error", err)
	}

	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")

	if board.URL == "" {
		writePlaceholder(w, board)
		return
	}
	http.Redirect(w, r, board.URL, http.StatusFound)
}

// writePlaceholder serves a board that has no URL assigned. A 200 with a
// readable page beats a 404 here: the machine is working, it has simply been
// given nothing to show, and an admin walking past the screen should be able
// to tell those apart from across the room. The poller sees no Location
// header and leaves the screen alone.
func writePlaceholder(w http.ResponseWriter, board *Board) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	name := html.EscapeString(board.Name)
	body := fmt.Sprintf(placeholderHTML, name, name)
	if _, err := w.Write([]byte(body)); err != nil {
		slog.Warn("writing kiosk placeholder", "board_id", board.ID, "error", err)
	}
}

// placeholderHTML is sized for a wall-mounted screen read at a distance: dark
// so it doesn't glare in a dim room, and it refreshes itself every 30s so an
// unattended screen picks up its first real URL without anyone rebooting it.
const placeholderHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="30">
<title>%s</title>
<style>
  html, body { height: 100%%; margin: 0; }
  body {
    display: flex; flex-direction: column;
    align-items: center; justify-content: center;
    background: #14161a; color: #d7dae0;
    font: 400 1rem/1.5 system-ui, -apple-system, "Segoe UI", sans-serif;
    text-align: center; padding: 2rem;
  }
  h1 { font-size: clamp(1.5rem, 5vw, 3rem); font-weight: 600; margin: 0 0 .75rem; }
  p { margin: 0; color: #8b919c; font-size: clamp(.9rem, 2vw, 1.25rem); }
</style>
</head>
<body>
  <h1>%s</h1>
  <p>No content assigned to this screen yet.</p>
</body>
</html>
`
