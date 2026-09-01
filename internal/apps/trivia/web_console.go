package trivia

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/console"
	"github.com/mrdon/kit/internal/auth"
)

// registerConsoleRoutes wires the host's JSON API behind /{slug}/api/trivia.
//
// Any member, not admin-only -- the same call the events and kiosk apps make.
// Running the quiz is operational work for whoever is behind the bar tonight,
// and making it wait on an admin is how a quiz night doesn't happen.
func registerConsoleRoutes(mux apps.Mux, a *App) {
	jsonRoute := func(h http.HandlerFunc) http.Handler {
		return console.JSON(a.pool, a.signer, h)
	}
	mux.Handle("GET /{slug}/api/trivia/games", jsonRoute(a.handleListGames))
	mux.Handle("POST /{slug}/api/trivia/games", jsonRoute(a.handleCreateGame))
	mux.Handle("GET /{slug}/api/trivia/games/{id}", jsonRoute(a.handleGetGame))
	mux.Handle("PATCH /{slug}/api/trivia/games/{id}", jsonRoute(a.handleUpdateGame))
	mux.Handle("DELETE /{slug}/api/trivia/games/{id}", jsonRoute(a.handleDeleteGame))
	mux.Handle("POST /{slug}/api/trivia/games/{id}/board", jsonRoute(a.handleBuildBoard))
	mux.Handle("POST /{slug}/api/trivia/games/{id}/action", jsonRoute(a.handleAction))
	mux.Handle("GET /{slug}/api/trivia/games/{id}/state", jsonRoute(a.handleHostState))
	mux.Handle("GET /{slug}/api/trivia/games/{id}/stream", jsonRoute(a.handleHostStream))
	mux.Handle("POST /{slug}/api/trivia/games/{id}/teams/{teamID}/reclaim", jsonRoute(a.handleReclaim))

	mux.Handle("GET /{slug}/api/trivia/questions", jsonRoute(a.handleListQuestions))
	mux.Handle("POST /{slug}/api/trivia/questions/import", jsonRoute(a.handleImport))
	mux.Handle("GET /{slug}/api/trivia/questions/sample", jsonRoute(a.handleSampleCSV))
	mux.Handle("POST /{slug}/api/trivia/questions/starter", jsonRoute(a.handleLoadStarter))
	mux.Handle("DELETE /{slug}/api/trivia/questions/{id}", jsonRoute(a.handleDeleteQuestion))
}

// gameJSON is the console's view of a game. join_url and the QR are served
// rather than assembled client-side so the console, the TV and the phone can
// never disagree about where a game lives.
type gameJSON struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Title     string    `json:"title"`
	Phase     string    `json:"phase"`
	JoinURL   string    `json:"join_url"`
	TVURL     string    `json:"tv_url"`
	Teams     int       `json:"teams"`
	Cells     int       `json:"cells"`
	Played    int       `json:"played"`
	Leader    string    `json:"leader"`
	CreatedAt time.Time `json:"created_at"`
	Settings  Settings  `json:"settings"`
}

func (a *App) gameToJSON(g *Game, slug string, teams, cells, played int, leader string) gameJSON {
	return gameJSON{
		ID: g.ID.String(), Name: g.Name, Title: g.Title, Phase: string(g.Phase),
		JoinURL: JoinURL(a.baseURL, slug, g.Name),
		TVURL:   JoinURL(a.baseURL, slug, g.Name) + "/tv",
		Teams:   teams, Cells: cells, Played: played, Leader: leader,
		CreatedAt: g.CreatedAt,
		Settings: Settings{
			Title: g.Title, BoardRows: g.BoardRows, BoardColumns: g.BoardColumns,
			CellValues: g.CellValues, TokenValues: g.TokenValues, FinalWager: g.FinalWager,
			AnswerSeconds: g.AnswerSeconds, RevealSeconds: g.RevealSeconds, BetSeconds: g.BetSeconds,
		},
	}
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
	for _, c := range snap.Board {
		cells++
		if c.Played {
			played++
		}
	}
	return teams, cells, played, leader
}

// createGameRequest carries only settings. The NAME is never client-supplied:
// it is the public URL contract and is drawn server-side so two hosts racing
// cannot claim the same one.
type createGameRequest struct {
	Settings Settings `json:"settings"`
}

func (a *App) handleCreateGame(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	var req createGameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		clientError(w, r, http.StatusBadRequest, "invalid JSON")
		return
	}
	s := normaliseSettings(req.Settings)
	if err := validateSettings(s); err != nil {
		clientError(w, r, http.StatusBadRequest, err.Error())
		return
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
	writeJSON(w, a.gameToJSON(game, tenant.Slug, 0, 0, 0, ""))
}

func (a *App) handleGetGame(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	game, ok := a.gameFromPath(w, r)
	if !ok {
		return
	}
	snap, err := a.svc.Snapshot(r.Context(), tenant.ID, game.ID)
	if err != nil {
		serverError(w, "loading trivia game", err)
		return
	}
	hist, err := TopicHistogram(r.Context(), a.pool, tenant.ID)
	if err != nil {
		serverError(w, "loading topic histogram", err)
		return
	}
	teams, cells, played, leader := a.gameCounts(r, game)
	writeJSON(w, map[string]any{
		"game":   a.gameToJSON(game, tenant.Slug, teams, cells, played, leader),
		"topics": hist,
		"state":  ProjectHost(snap),
	})
}

func (a *App) handleUpdateGame(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	game, ok := a.gameFromPath(w, r)
	if !ok {
		return
	}
	// Settings are frozen once the board is in play: changing the cell values
	// of a game people have already been reading off a TV would restate
	// scores that were announced out loud.
	if game.Phase != PhaseSetup && game.Phase != PhaseLobby {
		clientError(w, r, http.StatusConflict, "settings can only change before the game starts")
		return
	}
	var req createGameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		clientError(w, r, http.StatusBadRequest, "invalid JSON")
		return
	}
	s := normaliseSettings(req.Settings)
	if err := validateSettings(s); err != nil {
		clientError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := UpdateSettings(r.Context(), a.pool, tenant.ID, game.ID, s)
	if err != nil {
		serverError(w, "updating trivia settings", err)
		return
	}
	writeJSON(w, a.gameToJSON(updated, tenant.Slug, 0, 0, 0, ""))
}

func (a *App) handleDeleteGame(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	game, ok := a.gameFromPath(w, r)
	if !ok {
		return
	}
	if err := DeleteGame(r.Context(), a.pool, tenant.ID, game.ID); err != nil {
		serverError(w, "deleting trivia game", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAction is the ONE host endpoint. Every host click is the same shape --
// a guarded transition needing the same conflict check -- so a from_phase
// mismatch returns 409 and the console re-renders from its stream rather than
// silently skipping a question.
func (a *App) handleAction(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	game, ok := a.gameFromPath(w, r)
	if !ok {
		return
	}
	var req ActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		clientError(w, r, http.StatusBadRequest, "invalid JSON")
		return
	}
	snap, err := a.svc.Do(r.Context(), tenant.ID, game.ID, req)
	switch {
	case errors.Is(err, ErrPhaseConflict):
		clientError(w, r, http.StatusConflict, err.Error())
		return
	case errors.Is(err, ErrBadRequest):
		clientError(w, r, http.StatusBadRequest, err.Error())
		return
	case errors.Is(err, ErrNotFound):
		http.NotFound(w, r)
		return
	case err != nil:
		serverError(w, "running trivia action", err)
		return
	}
	writeJSON(w, ProjectHost(snap))
}

// handleReclaim mints a fresh identity for a table whose phone died, and
// returns the four digits the host reads out. The host is standing in the
// room and can see who is asking, which is exactly why this lives here and
// not on the phone as a "pick your team" list.
func (a *App) handleReclaim(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	game, ok := a.gameFromPath(w, r)
	if !ok {
		return
	}
	teamID, err := uuid.Parse(r.PathValue("teamID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	code := ReclaimCode()
	if err := a.svc.IssueReclaim(r.Context(), tenant.ID, game.ID, teamID, code); err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		serverError(w, "issuing trivia reclaim code", err)
		return
	}
	writeJSON(w, map[string]string{"code": code})
}

func (a *App) gameFromPath(w http.ResponseWriter, r *http.Request) (*Game, bool) {
	tenant := auth.TenantFromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		clientError(w, r, http.StatusNotFound, "not a game id")
		return nil, false
	}
	game, err := GetGame(r.Context(), a.pool, tenant.ID, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			clientError(w, r, http.StatusNotFound, "no such game")
			return nil, false
		}
		serverError(w, "loading trivia game", err)
		return nil, false
	}
	return game, true
}

func callerID(r *http.Request) *uuid.UUID {
	caller := auth.CallerFromContext(r.Context())
	if caller == nil {
		return nil
	}
	id := caller.UserID
	return &id
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("writing trivia json", "error", err)
	}
}

func serverError(w http.ResponseWriter, what string, err error) {
	slog.Error(what, "error", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

// clientError answers a 4xx AND logs it.
//
// The logging half is the point. A rejected request that leaves no
// server-side trace is undebuggable from the operator's side: somebody says
// "I got a 400 saving the settings", the logs are silent, and the only way to
// find out which of a dozen validation rules fired is to reproduce it by
// hand. Ask any of these handlers to refuse something and it says so out
// loud, with the route and the reason.
//
// WARN, not ERROR: the request was refused on purpose and the service is
// healthy. It should not page anyone, but it must be greppable.
func clientError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	slog.Warn("trivia request refused",
		"status", status,
		"method", r.Method,
		"route", r.Pattern,
		"path", r.URL.Path,
		"reason", msg,
	)
	http.Error(w, msg, status)
}
