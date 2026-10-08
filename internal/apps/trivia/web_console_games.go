package trivia

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/auth"
)

// The game list and game creation: the console's view of a game, and the
// one button that starts a night. Split from web_console.go, which keeps
// the routes and the per-game handlers.

// gameJSON is the console's view of a game. The URLs are served rather than
// assembled client-side so the console, the TV and the phone can never
// disagree about where a game lives.
type gameJSON struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Title   string `json:"title"`
	Phase   string `json:"phase"`
	JoinURL string `json:"join_url"`
	// ShortURL is the same destination in a third of the characters. It is
	// what the QR encodes and what the TV prints, so it is what a host reads
	// out when somebody's camera will not focus.
	ShortURL string `json:"short_url"`
	// ScreenURL is the STABLE address — it always shows the newest game, and
	// it is what a host should put on the TV. TVURL pins one specific game
	// and is the exception, useful for looking at an old night or running two
	// rooms at once.
	ScreenURL string    `json:"screen_url"`
	TVURL     string    `json:"tv_url"`
	Teams     int       `json:"teams"`
	Cells     int       `json:"cells"`
	Played    int       `json:"played"`
	Leader    string    `json:"leader"`
	CreatedAt time.Time `json:"created_at"`
	Settings  Settings  `json:"settings"`
	// BoardError is set when a settings change redrew the board and the
	// bank could not fill the new shape; the board is empty until the host
	// fixes the supply or the shape.
	BoardError string `json:"board_error,omitempty"`
}

func (a *App) gameToJSON(g *Game, slug string, teams, cells, played int, leader string) gameJSON {
	return gameJSON{
		ID: g.ID.String(), Name: g.Name, Title: g.Title, Phase: string(g.Phase),
		JoinURL:   JoinURL(a.baseURL, slug, g.Name),
		ShortURL:  shortURLOrLong(a.baseURL, slug, g),
		ScreenURL: strings.TrimRight(a.baseURL, "/") + "/" + slug + "/trivia/tv",
		TVURL:     JoinURL(a.baseURL, slug, g.Name) + "/tv",
		Teams:     teams, Cells: cells, Played: played, Leader: leader,
		CreatedAt: g.CreatedAt,
		Settings:  SettingsOf(g),
	}
}

// shortURLOrLong prefers the short link, falling back to the readable one for
// a row that predates join codes and somehow escaped the backfill.
func shortURLOrLong(baseURL, slug string, g *Game) string {
	if g.JoinCode != "" {
		return ShortJoinURL(baseURL, g.JoinCode)
	}
	return JoinURL(baseURL, slug, g.Name)
}

func (a *App) handleListGames(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	games, err := ListGames(r.Context(), a.pool, tenant.ID, 50)
	if err != nil {
		serverError(w, "listing trivia games", err)
		return
	}
	out := make([]gameJSON, 0, len(games))
	for _, g := range games {
		teams, cells, played, leader := a.gameCounts(r, g)
		out = append(out, a.gameToJSON(g, tenant.Slug, teams, cells, played, leader))
	}
	writeJSON(w, map[string]any{"games": out})
}

// gameCounts fills the list row's summary. Errors are swallowed to zero: a
// list of games must render even if one game's board is unreadable.
func (a *App) gameCounts(r *http.Request, g *Game) (teams, cells, played int, leader string) {
	snap, err := a.svc.Snapshot(r.Context(), g.TenantID, g.ID)
	if err != nil {
		return 0, 0, 0, ""
	}
	teams = len(snap.Teams)
	best := -1
	for _, t := range snap.Teams {
		if t.Score > best {
			best, leader = t.Score, t.Name
		}
	}
	// Every round of it. snap.Board is the round in play only.
	cells, played = snap.CellsTotal, snap.CellsPlayed
	return teams, cells, played, leader
}

// createGameRequest carries only settings, and they are OPTIONAL. The NAME is
// never client-supplied: it is the public URL contract and is drawn
// server-side so two hosts racing cannot claim the same one.
//
// A nil Settings means "same as last time" — see handleCreateGame.
type createGameRequest struct {
	Settings *Settings `json:"settings"`
}

func (a *App) handleCreateGame(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	var req createGameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		clientError(w, r, http.StatusBadRequest, "invalid JSON")
		return
	}
	// A new game inherits the previous one's setup. A venue runs the same
	// quiz every week; retyping the board shape, the values and the timers
	// each time is a chore with no upside, and the host can still change
	// anything on the setup page.
	s, err := a.settingsForNewGame(r, tenant.ID, req.Settings)
	if err != nil {
		serverError(w, "reading previous trivia settings", err)
		return
	}
	if err := validateSettings(s); err != nil {
		clientError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	// A game always has a human name. The slug is a URL token, never a label:
	// letting the title be empty means every surface needs a fallback, and
	// the fallback is the slug, which is meaningless to anybody in the room.
	if strings.TrimSpace(s.Title) == "" {
		s.Title = defaultGameTitle()
	}
	name, err := UniqueName(r.Context(), a.pool, tenant.ID)
	if err != nil {
		serverError(w, "picking a trivia game name", err)
		return
	}
	game, err := CreateGame(r.Context(), a.pool, tenant.ID, name, s, callerID(r))
	if err != nil {
		serverError(w, "creating trivia game", err)
		return
	}
	// Build a board straight away. A game with no board is not a game, and
	// making the host press Auto before anything works is a step that only
	// ever has one sensible answer. They can rebuild or choose the columns
	// themselves on the setup page; this is the default, not a decision.
	//
	// Best-effort on purpose: a workspace with no questions yet gets a game
	// with an empty board and a setup page telling it what to do, which is
	// better than refusing to create the game at all.
	if err := a.autoBuildBoard(r, tenant.ID, game); err != nil {
		slog.Info("trivia: could not auto-build a board for a new game",
			"game_id", game.ID, "reason", err)
	}
	teams, cells, played, leader := a.gameCounts(r, game)
	writeJSON(w, a.gameToJSON(game, tenant.Slug, teams, cells, played, leader))
}

// autoBuildBoard fills a new game's board from whatever questions the
// workspace has. Errors are informational: "not enough questions yet" is a
// normal state for a fresh workspace, not a failure to create a game.
func (a *App) autoBuildBoard(r *http.Request, tenantID uuid.UUID, game *Game) error {
	datasetIDs, err := GameDatasetIDs(r.Context(), a.pool, tenantID, game.ID)
	if err != nil {
		return err
	}
	topics, err := a.resolveTopics(r, tenantID, game, buildBoardRequest{Auto: true}, datasetIDs)
	if err != nil {
		return err
	}
	cells, err := a.assignBoard(r, tenantID, game, topics, datasetIDs)
	if err != nil {
		return err
	}
	return ReplaceBoard(r.Context(), a.pool, tenantID, game.ID, cells)
}

// settingsForNewGame resolves what a new game starts as: whatever the client
// asked for, else the most recent game's settings, else the shipped defaults.
func (a *App) settingsForNewGame(r *http.Request, tenantID uuid.UUID, asked *Settings) (Settings, error) {
	if asked != nil {
		return normaliseSettings(*asked), nil
	}
	games, err := ListGames(r.Context(), a.pool, tenantID, 1)
	if err != nil {
		return Settings{}, err
	}
	if len(games) == 0 {
		return DefaultSettings(), nil
	}
	g := games[0]
	// The TITLE is not inherited. Everything else describes how the game is
	// played and is stable week to week; the title names one night.
	s := SettingsOf(g)
	s.Title = ""
	return normaliseSettings(s), nil
}

// defaultGameTitle names a night when the host has not. Dated, so a list of
// them is scannable rather than a column of identical labels.
func defaultGameTitle() string {
	return "Quiz night, " + time.Now().Format("2 Jan")
}
