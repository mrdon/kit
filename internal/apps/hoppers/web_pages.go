package hoppers

import (
	"io"
	"log/slog"
	"net/http"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/livegame"
	consoleassets "github.com/mrdon/kit/web/console"
)

// handleShortJoin turns /j/h/{code} into the real phone page. A redirect
// rather than serving the page here, so there is one canonical address: the
// cookie is Path-scoped to /{slug}/hoppers/{game}.
func (a *App) handleShortJoin(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if !livegame.IsValidJoinCode(code) {
		http.NotFound(w, r)
		return
	}
	game, slug, err := GameByJoinCode(r.Context(), a.pool, code)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// No {slug} on this route, so the enablement gate cannot wrap it.
	if !apps.IsEnabled(r.Context(), game.TenantID, AppName) {
		http.NotFound(w, r)
		return
	}
	// no-store: a cached redirect would pin a phone to a finished game.
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	http.Redirect(w, r, JoinURL(slug, game.Name), http.StatusFound)
}

// handleDisplay serves one game's TV page.
func (a *App) handleDisplay(w http.ResponseWriter, r *http.Request) {
	game, slug, ok := a.resolveGame(w, r)
	if !ok {
		return
	}
	a.writeDisplay(w, game, slug, false)
}

// handleLatestDisplay serves the newest game's TV page from a fixed address,
// so a screen (or the kiosk) can be pointed at /{slug}/hoppers/tv once and
// left alone. "Newest" is the most recently created game: the dumbest rule
// that works, and one a host never has to hold in their head.
func (a *App) handleLatestDisplay(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.NotFound(w, r)
		return
	}
	games, err := ListGames(r.Context(), a.pool, tenant.ID, 1)
	if err != nil {
		serverError(w, "finding the latest hoppers game", err)
		return
	}
	if len(games) == 0 {
		livegame.WriteHTML(w, livegame.NoGamePage("Hoppers"))
		return
	}
	a.writeDisplay(w, games[0], tenant.Slug, true)
}

func (a *App) writeDisplay(w http.ResponseWriter, game *Game, slug string, followLatest bool) {
	html, err := RenderDisplay(a.baseURL, slug, game, followLatest)
	if err != nil {
		serverError(w, "rendering hoppers display", err)
		return
	}
	livegame.WriteHTML(w, html)
}

// handleTVVersion is the TV page's reload stamp (livegame.DisplayVersion),
// for one game or, without {game}, for whichever is newest.
func (a *App) handleTVVersion(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.NotFound(w, r)
		return
	}
	var game *Game
	if name := r.PathValue("game"); name != "" {
		g, err := GetGameByName(r.Context(), a.pool, tenant.ID, name)
		if !livegame.IsValidGameName(name) || err != nil {
			http.NotFound(w, r)
			return
		}
		game = g
	} else {
		games, err := ListGames(r.Context(), a.pool, tenant.ID, 1)
		if err != nil {
			serverError(w, "finding the latest hoppers game", err)
			return
		}
		if len(games) > 0 {
			game = games[0]
		}
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	if _, err := io.WriteString(w, displayVersion(game)); err != nil {
		slog.Warn("writing hoppers display version", "error", err)
	}
}

func displayVersion(game *Game) string {
	if game == nil {
		return livegame.DisplayVersion(nil, "")
	}
	return livegame.DisplayVersion(&game.CreatedAt, game.Title)
}

// handlePlayerPage serves the phone's SPA shell (the console build's
// hoppers entry).
func (a *App) handlePlayerPage(w http.ResponseWriter, r *http.Request) {
	game, slug, ok := a.resolveGame(w, r)
	if !ok {
		return
	}
	title := game.Title
	if title == "" {
		title = "Hoppers · " + game.Name
	}
	body, err := consoleassets.HoppersHTML(slug, title)
	if err != nil {
		http.Error(w, "Kit console not built. Run `make console-build`.", http.StatusServiceUnavailable)
		return
	}
	livegame.WriteHTML(w, body)
}
