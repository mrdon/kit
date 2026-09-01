package trivia

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/auth"
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
	html, err := RenderDisplay(a.baseURL, slug, game, followLatest)
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
	_ = r
}

// handleShortJoin turns a short code into the real join page.
//
// A redirect rather than serving the page here, so there is one canonical
// address a phone ends up on: the cookie is Path-scoped to
// /{slug}/trivia/{game}, and a page served under /j/{code} would be outside
// that scope and unable to hold an identity at all.
func (a *App) handleShortJoin(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if !IsValidJoinCode(code) {
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
		writeNoGamePlaceholder(w)
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
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	if _, err := io.WriteString(w, displayVersion(game)); err != nil {
		slog.Warn("writing trivia display version", "error", err)
	}
}

// displayVersion stamps the rendered page, in the menu board's style: a short
// readable token the screen compares against its own.
//
// CREATED_AT is the primary input, and it is the right one precisely because
// it is IMMUTABLE. The game's updated_at bumps on every answer and every
// chip, so stamping that would reload the TV several times a round; created_at
// moves only when a genuinely different game becomes the newest, which is
// exactly when a screen parked on the stable address needs to repaint.
//
// The title rides along because it is rendered into the HTML — renaming the
// night has to reach the screen too — and it is the only other thing the
// server-rendered page shows that a host can change mid-life.
//
// "empty" is a real value: a screen on the stable address with no game yet
// must notice the first one.
func displayVersion(game *Game) string {
	if game == nil {
		return "empty"
	}
	v := strconv.FormatInt(game.CreatedAt.UnixNano(), 36)
	if game.Title != "" {
		sum := sha256.Sum256([]byte(game.Title))
		v += "-" + hex.EncodeToString(sum[:3])
	}
	return v
}

// writeNoGamePlaceholder is the stable TV URL before any game exists.
func writeNoGamePlaceholder(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(noGameHTML)); err != nil {
		slog.Warn("writing trivia placeholder", "error", err)
	}
}

// A meta refresh is safe HERE and nowhere else in this app: there is no SSE
// stream on this page to tear down, and it is what lets a screen hung on the
// wall pick up the first game without anyone rebooting it.
const noGameHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="15">
<title>Trivia</title>
<style>
  html,body{height:100%;margin:0}
  body{display:flex;align-items:center;justify-content:center;text-align:center;
       background:linear-gradient(160deg,#1b2b38,#14222d);color:#b9c4cf;
       font:400 1rem/1.5 system-ui,-apple-system,"Segoe UI",sans-serif;padding:2rem}
  h1{font-size:clamp(2rem,6vw,4.5rem);margin:0 0 .5rem;color:#f4efe7}
  p{margin:0;font-size:clamp(1rem,2.2vw,1.6rem);color:#8ea2b3}
</style>
</head>
<body><div>
  <h1>No quiz tonight</h1>
  <p>This screen is working. Create a game and it will appear here.</p>
</div></body>
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(body); err != nil {
		slog.Warn("writing trivia player page", "game_id", game.ID, "error", err)
	}
}
