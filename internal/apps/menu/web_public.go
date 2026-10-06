package menu

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/auth"
)

// registerPublicRoutes mounts the unauthenticated routes this app exists for.
// They carry {slug}, so the enablement gate in apps.RegisterAllRoutes 404s
// them automatically for a workspace that has turned the menu app off.
//
// Only TenantFromPath wraps them: no session middleware, no CSRF header, no
// caller. The screen showing this page has no credentials to present, and the
// content is a list of beers already painted ten feet tall on a wall.
//
// One address per workspace, with no key in it. There is one menu.
func registerPublicRoutes(mux apps.Mux, a *App) {
	tenantMW := auth.TenantFromPath(a.pool)
	mux.Handle("GET /{slug}/menu", tenantMW(http.HandlerFunc(a.handleBoard)))
	// A few bytes the page polls to decide whether it is out of date. Without
	// it a screen that loaded once would show that tap list until someone
	// power-cycled the TV, however often the menu refreshed.
	mux.Handle("GET /{slug}/menu.version", tenantMW(http.HandlerFunc(a.handleVersion)))
}

// handleBoard renders the menu for a screen.
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

	row, err := a.svc.Get(r.Context(), tenant.ID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// The menu always answers, even before anyone has set a tap list —
			// a 404 here would mean the address could not be wired to a screen
			// until content existed, which is backwards.
			writePlaceholder(w)
			return
		}
		slog.Error("loading menu board", "tenant_id", tenant.ID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Ask upstream before parsing: a refresh replaces the payload, so parsing
	// first would be work thrown away on every stale hit.
	row = a.EnsureFresh(r.Context(), tenant.ID, row)

	board, err := ParseBoard(row.Payload)
	if err != nil {
		// A stored board that no longer parses means the schema moved under a
		// payload that was valid when it was written. Log loudly and 500: a
		// half-rendered tap list on a wall is worse than a blank screen,
		// because staff will pour from it.
		slog.Error("stored menu board failed to parse", "tenant_id", tenant.ID, "error", err)
		http.Error(w, "menu board is not renderable", http.StatusInternalServerError)
		return
	}

	assets, err := LoadAssets(r.Context(), a.pool, tenant.ID)
	if err != nil {
		slog.Error("loading menu assets", "tenant_id", tenant.ID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	hh := a.happyHourFor(r.Context(), tenant.ID)
	loc := locationOf(tenant.Timezone)
	if now := timeNow(); hh != nil && hh.OnAt(now, loc) {
		board.HappyBanner = applyHappyHour(board, &hh.Config, hh.Config.Until(now, loc))
	}
	gr := a.glutenReducedFor(r.Context(), tenant.ID)
	applyGlutenReduced(board, gr)

	html, err := Render(board, assets, liveVersion(row, hh, gr, loc))
	if err != nil {
		slog.Error("rendering menu board", "tenant_id", tenant.ID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(html)); err != nil {
		slog.Warn("writing menu board", "tenant_id", tenant.ID, "error", err)
	}
}

// handleVersion answers with the menu's current version stamp.
//
// Deliberately tiny and separate from the page: re-fetching 150KB every thirty
// seconds to discover nothing changed is the kind of thing that is invisible
// in testing and obvious on a metered connection. It is also what drives the
// refresh, so the expensive check happens on a few-byte request.
func (a *App) handleVersion(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.NotFound(w, r)
		return
	}
	version := "empty"
	row, err := a.svc.Get(r.Context(), tenant.ID)
	switch {
	case err == nil:
		hh := a.happyHourFor(r.Context(), tenant.ID)
		gr := a.glutenReducedFor(r.Context(), tenant.ID)
		version = liveVersion(a.EnsureFresh(r.Context(), tenant.ID, row), hh, gr, locationOf(tenant.Timezone))
	case !errors.Is(err, ErrNotFound):
		slog.Error("reading menu version", "tenant_id", tenant.ID, "error", err)
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
// the tap list actually changes — a refresh that finds nothing new touches
// synced_at instead — so a quiet menu never reloads a screen.
//
// The render stamp is the second half of it, because "has the board changed"
// is not the same question as "has the tap list changed". A deploy that
// touches only the stylesheet leaves updated_at alone, and a screen that
// never notices keeps painting with the CSS it booted with.
//
// The badge count is the third, and it is there because a "New" badge expires
// on a tap list nobody touched -- see newTapCount. It sits in the middle
// rather than on the end so the render stamp stays the suffix, which is what
// makes a version readable at a glance when a screen is misbehaving.
func boardVersion(row *BoardRow) string {
	return strconv.FormatInt(row.UpdatedAt.UnixNano(), 36) +
		"." + strconv.Itoa(newTapCount(row.Payload)) +
		"." + RenderStamp()
}

// liveVersion is the stamp the wall compares against: the board's own, with
// the happy hour's state in front so the screen reloads when happy hour
// starts and ends, and when its setting changes, and the gluten reduced list
// so marking a beer reaches the wall. In front rather than behind, so the
// render stamp stays the suffix.
func liveVersion(row *BoardRow, hh *HappyHourState, gr []string, loc *time.Location) string {
	v := boardVersion(row)
	if s := glutenStamp(gr); s != "" {
		v = s + "." + v
	}
	if s := happyStamp(hh, timeNow(), loc); s != "" {
		v = s + "." + v
	}
	return v
}

// glutenReducedFor loads the gluten reduced list for the wall. A failure to
// load costs the badges, never the tap list.
func (a *App) glutenReducedFor(ctx context.Context, tenantID uuid.UUID) []string {
	beers, err := LoadGlutenReduced(ctx, a.pool, tenantID)
	if err != nil {
		slog.Warn("loading gluten reduced beers for the board", "tenant_id", tenantID, "error", err)
		return nil
	}
	return beers
}

// happyHourFor loads the happy hour the wall should apply, or nil when there
// is none. A failure to load costs the banner, never the tap list: the
// regular prices are still true.
func (a *App) happyHourFor(ctx context.Context, tenantID uuid.UUID) *HappyHourState {
	state, err := LoadHappyHour(ctx, a.pool, tenantID)
	if err != nil {
		slog.Warn("loading happy hour for the board", "tenant_id", tenantID, "error", err)
		return nil
	}
	if !state.Configured {
		return nil
	}
	return state
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
