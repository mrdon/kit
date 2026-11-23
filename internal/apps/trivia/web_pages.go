package trivia

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/livegame"
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
	a.writeDisplay(w, r, game, slug, false)
}

// writeDisplay renders one game's TV page. Shared by the per-game address and
// the stable latest-game one, so the two can never drift.
func (a *App) writeDisplay(w http.ResponseWriter, r *http.Request, game *Game, slug string, followLatest bool) {
	explainer, err := GetExplainerLink(r.Context(), a.pool, game.TenantID)
	if err != nil {
		// The lobby still works without its video; never blank the TV for it.
		slog.Warn("loading trivia explainer link", "game_id", game.ID, "error", err)
	}
	render := RenderDisplay
	if isOverlay(r) {
		render = RenderOverlay
	}
	html, err := render(a.baseURL, slug, game, followLatest, explainer)
	if err != nil {
		serverError(w, "rendering trivia display", err)
		return
	}
	livegame.WriteHTML(w, []byte(html))
}

// isOverlay reports whether the TV page was asked for as the OBS overlay
// (?overlay=1): the same game and stream, drawn as an L with a transparent
// hole for the broadcast. See render_overlay.go.
func isOverlay(r *http.Request) bool {
	v := r.URL.Query().Get("overlay")
	return v != "" && v != "0" && v != "false"
}

// handleShortJoin turns a short code into the real join page.
//
// A redirect rather than serving the page here, so there is one canonical
// address a phone ends up on: the cookie is Path-scoped to
// /{slug}/trivia/{game}, and a page served under /j/{code} would be outside
// that scope and unable to hold an identity at all.
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
	// This route carries no {slug}, so the enablement gate in
	// apps.RegisterAllRoutes cannot wrap it. Check here, once the code has
	// told us whose game it is.
	if !apps.IsEnabled(r.Context(), game.TenantID, AppName) {
		http.NotFound(w, r)
		return
	}
	// no-store for the same reason the pages themselves use it: a cached
	// redirect would pin a phone to a game that has finished.
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	http.Redirect(w, r, "/"+slug+"/trivia/"+game.Name, http.StatusFound)
}

// handleLatestDisplay serves the newest game's TV page from a fixed address,
// so a screen can be pointed at /{slug}/trivia/tv once and left alone.
//
// "Newest" is simply the most recently created game — deliberately the
// dumbest rule that works. Anything cleverer ("the one in play, unless...")
// is a rule the host has to hold in their head while standing in a bar, and
// the failure mode of a clever rule is a screen showing the wrong night with
// no way to tell why.
func (a *App) handleLatestDisplay(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.NotFound(w, r)
		return
	}
	games, err := ListGames(r.Context(), a.pool, tenant.ID, 1)
	if err != nil {
		serverError(w, "finding the latest trivia game", err)
		return
	}
	if len(games) == 0 {
		// A 200 with a readable page beats a 404: the screen is working and
		// correctly addressed, there is simply no game yet. It polls itself
		// so the first game appears without anyone touching the TV.
		writeNoGamePlaceholder(w, isOverlay(r))
		return
	}
	a.writeDisplay(w, r, games[0], tenant.Slug, true)
}

// handleTVVersion answers with a stamp of everything the SERVER-RENDERED page
// depends on, following the menu board's pattern: a few bytes the screen
// polls, and it reloads when they change.
//
// A stream is not enough on its own. The SSE frames carry the live state, but
// the QR code, the join words, the fonts and the heading are baked into the
// HTML at render time — so a renamed night, or a newer game appearing on the
// stable address, leaves a screen showing something that is simply wrong
// until somebody walks over to it.
//
// WHAT THE STAMP DELIBERATELY EXCLUDES is the game's updated_at, which bumps
// on every answer and every chip. Stamping that would reload the TV in the
// middle of a question, several times a round. The stamp covers identity and
// the rendered chrome only: which game, its name, its title.
func (a *App) handleTVVersion(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.NotFound(w, r)
		return
	}
	var game *Game
	if name := r.PathValue("game"); name != "" {
		if !IsValidGameName(name) {
			http.NotFound(w, r)
			return
		}
		g, err := GetGameByName(r.Context(), a.pool, tenant.ID, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		game = g
	} else {
		games, err := ListGames(r.Context(), a.pool, tenant.ID, 1)
		if err != nil {
			serverError(w, "finding the latest trivia game", err)
			return
		}
		if len(games) > 0 {
			game = games[0]
		}
	}
	explainer, err := GetExplainerLink(r.Context(), a.pool, tenant.ID)
	if err != nil {
		slog.Warn("loading trivia explainer link", "tenant_id", tenant.ID, "error", err)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	if _, err := io.WriteString(w, displayVersion(game)+explainerStamp(explainer)); err != nil {
		slog.Warn("writing trivia display version", "error", err)
	}
}

// displayVersion stamps the rendered page (livegame.DisplayVersion): the
// game's immutable created_at, its title, and the build. The game's
// updated_at is deliberately left out -- it bumps on every answer and every
// chip, and stamping it would reload the TV several times a round.
func displayVersion(game *Game) string {
	if game == nil {
		return livegame.DisplayVersion(nil, "")
	}
	return livegame.DisplayVersion(&game.CreatedAt, game.Title)
}

// explainerStamp adds the lobby video's link to the version, so adding,
// changing or removing it reloads the screens. A new version of the same
// Drive file keeps the link and needs no reload: the TV revalidates the
// video itself.
func explainerStamp(link string) string {
	if link == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(link))
	return "-x" + hex.EncodeToString(sum[:3])
}

// writeNoGamePlaceholder is the stable TV URL before any game exists.
//
// The overlay gets a page that paints NOTHING. It sits on top of the
// broadcast in OBS, and an opaque "no game yet" card there would black out
// the game for the whole bar until somebody created a night.
func writeNoGamePlaceholder(w http.ResponseWriter, overlay bool) {
	page := livegame.NoGamePage("Trivia")
	if overlay {
		page = []byte(noGameOverlayHTML)
	}
	livegame.WriteHTML(w, page)
}

// noGameOverlayHTML is the overlay's placeholder: transparent, and polling
// for the first game the way livegame.NoGamePage does.
const noGameOverlayHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta http-equiv="refresh" content="15">
<title>Trivia overlay</title>
<style>html,body{margin:0;background:transparent}</style>
</head>
<body></body>
</html>
`

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
	livegame.WriteHTML(w, body)
}
