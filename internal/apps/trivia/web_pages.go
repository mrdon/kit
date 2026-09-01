package trivia

import (
	"log/slog"
	"net/http"

	consoleassets "github.com/mrdon/kit/web/console"
)

// handleDisplay serves the TV page.
//
// Cache-Control: no-store is load-bearing rather than hygiene. This page's
// whole purpose is to change, and an intermediary that caches it pins a wall
// panel to a game that finished last Tuesday with nothing on the admin side
// to explain why.
func (a *App) handleDisplay(w http.ResponseWriter, r *http.Request) {
	game, slug, ok := a.resolveGame(w, r)
	if !ok {
		return
	}
	html, err := RenderDisplay(a.baseURL, slug, game)
	if err != nil {
		serverError(w, "rendering trivia display", err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(html)); err != nil {
		slog.Warn("writing trivia display", "game_id", game.ID, "error", err)
	}
}

// handlePlayerPage serves the phone's SPA shell.
//
// The opposite call from the TV, on purpose. A phone is genuinely
// interactive -- numeric entry with live validation, chip placement with
// undo, optimistic submit with rollback, eight stateful screens -- which is
// what React and framer-motion are for. The TV is render(state) over seven
// screens of big CSS transforms, where they would earn nothing.
func (a *App) handlePlayerPage(w http.ResponseWriter, r *http.Request) {
	game, slug, ok := a.resolveGame(w, r)
	if !ok {
		return
	}
	title := game.Title
	if title == "" {
		title = "Trivia · " + game.Name
	}
	body, err := consoleassets.PlayHTML(slug, title)
	if err != nil {
		http.Error(w, "Kit console not built. Run `make console-build`.", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(body); err != nil {
		slog.Warn("writing trivia player page", "game_id", game.ID, "error", err)
	}
}
