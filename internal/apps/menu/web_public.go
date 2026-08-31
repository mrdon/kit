package menu

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

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
	// A few bytes the page polls to decide whether it is out of date. Without
	// it a screen that loaded once would show that tap list until someone
	// power-cycled the TV, however often the server synced.
	mux.Handle("GET /{slug}/menu/{key}/version", tenantMW(http.HandlerFunc(a.handleVersion)))
	mux.Handle("GET /{slug}/menu.version", tenantMW(http.HandlerFunc(a.handleVersion)))
}

// handleVersion answers with the board's current version stamp.
//
// Deliberately tiny and separate from the page: re-fetching 150KB every
// thirty seconds to discover nothing changed is the kind of thing that is
// invisible in testing and obvious on a metered connection.
func (a *App) handleVersion(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.NotFound(w, r)
		return
	}
	key := r.PathValue("key")
	if key == "" {
		key = DefaultKey
	}
	version := "empty"
	if row, err := a.svc.Get(r.Context(), tenant.ID, key); err == nil {
		// The screen polling this is what drives the pull. Doing it here
		// rather than only on render means the expensive check happens on a
		// few-byte request, and the page itself almost always finds the tap
		// list already fresh.
		version = boardVersion(a.EnsureFresh(r.Context(), tenant.ID, row))
	} else if !errors.Is(err, ErrNotFound) {
		slog.Error("reading menu version", "tenant_id", tenant.ID, "key", key, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	if _, err := io.WriteString(w, version); err != nil {
		slog.Warn("writing menu version", "error", err)
	}
}

// boardVersion is what the page compares against. updated_at moves only when
// the tap list actually changes -- a sync that finds nothing new touches
// synced_at instead -- so a quiet board never reloads a screen.
func boardVersion(row *BoardRow) string {
	return strconv.FormatInt(row.UpdatedAt.UnixNano(), 36)
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

	// Ask upstream before parsing: the refresh replaces the payload, so
	// parsing first would be work thrown away on every stale hit.
	row = a.EnsureFresh(r.Context(), tenant.ID, row)

	board, err := ParseBoard(row.Payload)
	if err != nil {
		// A stored board that no longer parses means the schema moved under a
		// payload that was valid when it was written. Log loudly and 500: a
		// half-rendered tap list on a wall is worse than a blank screen,
		// because staff will pour from it.
		slog.Error("stored menu board failed to parse",
			"tenant_id", tenant.ID, "key", key, "error", err)
		http.Error(w, "menu board is not renderable", http.StatusInternalServerError)
		return
	}

	assets, err := LoadAssets(r.Context(), a.pool, tenant.ID)
	if err != nil {
		slog.Error("loading menu assets", "tenant_id", tenant.ID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	html, err := Render(board, assets, boardVersion(row))
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
