package trivia

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

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
//
// A paired device holding trivia.host (the trivia laptop) gets every route
// here, dataset management and deletes included: it IS the quiz machine.
func registerConsoleRoutes(mux apps.Mux, a *App) {
	jsonRoute := func(h http.HandlerFunc) http.Handler {
		return console.RequireCap(a.pool, a.signer, auth.CapTriviaHost, h)
	}
	mux.Handle("GET /{slug}/api/trivia/games", jsonRoute(a.handleListGames))
	mux.Handle("POST /{slug}/api/trivia/games", jsonRoute(a.handleCreateGame))
	mux.Handle("GET /{slug}/api/trivia/games/{id}", jsonRoute(a.handleGetGame))
	mux.Handle("PATCH /{slug}/api/trivia/games/{id}", jsonRoute(a.handleUpdateGame))
	mux.Handle("DELETE /{slug}/api/trivia/games/{id}", jsonRoute(a.handleDeleteGame))
	mux.Handle("DELETE /{slug}/api/trivia/games", jsonRoute(a.handleDeleteAllGames))
	mux.Handle("POST /{slug}/api/trivia/games/{id}/board", jsonRoute(a.handleBuildBoard))
	mux.Handle("POST /{slug}/api/trivia/games/{id}/board/cells/{cellID}/swap", jsonRoute(a.handleSwapCell))
	mux.Handle("POST /{slug}/api/trivia/games/{id}/action", jsonRoute(a.handleAction))
	mux.Handle("GET /{slug}/api/trivia/games/{id}/state", jsonRoute(a.handleHostState))
	mux.Handle("GET /{slug}/api/trivia/games/{id}/stream", jsonRoute(a.handleHostStream))
	mux.Handle("POST /{slug}/api/trivia/games/{id}/teams/{teamID}/reclaim", jsonRoute(a.handleReclaim))

	mux.Handle("GET /{slug}/api/trivia/questions", jsonRoute(a.handleListQuestions))
	mux.Handle("GET /{slug}/api/trivia/datasets", jsonRoute(a.handleListDatasets))
	mux.Handle("PATCH /{slug}/api/trivia/datasets/{id}", jsonRoute(a.handleRenameDataset))
	mux.Handle("DELETE /{slug}/api/trivia/datasets/{id}", jsonRoute(a.handleDeleteDataset))
	mux.Handle("PUT /{slug}/api/trivia/games/{id}/datasets", jsonRoute(a.handleSetGameDatasets))
	mux.Handle("POST /{slug}/api/trivia/questions/import", jsonRoute(a.handleImport))
	mux.Handle("GET /{slug}/api/trivia/questions/sample", jsonRoute(a.handleSampleCSV))
	mux.Handle("POST /{slug}/api/trivia/questions/starter", jsonRoute(a.handleLoadStarter))
	mux.Handle("POST /{slug}/api/trivia/questions/packs/{key}", jsonRoute(a.handleLoadStarter))
	mux.Handle("DELETE /{slug}/api/trivia/questions/{id}", jsonRoute(a.handleDeleteQuestion))

	// Where ratings go is a workspace setting, so for people it is
	// admin-only where the rest of this API is not. The device still
	// passes: the capability is the whole of what it may do.
	adminRoute := func(h http.HandlerFunc) http.Handler {
		return console.RequireCapAdmin(a.pool, a.signer, auth.CapTriviaHost, h)
	}
	mux.Handle("GET /{slug}/api/trivia/feedback-channel", adminRoute(a.handleGetFeedbackChannel))
	mux.Handle("PUT /{slug}/api/trivia/feedback-channel", adminRoute(a.handleSaveFeedbackChannel))
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
	// Everything the setup page shows is scoped to what THIS game draws from,
	// so the column picker can never offer a topic the game cannot fill.
	selected, err := GameDatasetIDs(r.Context(), a.pool, tenant.ID, game.ID)
	if err != nil {
		serverError(w, "loading game datasets", err)
		return
	}
	hist, err := TopicHistogram(r.Context(), a.pool, tenant.ID, selected, freshnessOf(game))
	if err != nil {
		serverError(w, "loading topic histogram", err)
		return
	}
	sets, err := ListDatasets(r.Context(), a.pool, tenant.ID)
	if err != nil {
		serverError(w, "listing trivia datasets", err)
		return
	}
	ids := make([]string, 0, len(selected))
	for _, id := range selected {
		ids = append(ids, id.String())
	}
	// The questions BEHIND the board, which the host frame deliberately does
	// not carry (see web_console_board.go). This is the page where a host
	// reads their own board before the doors open, so it is the page that
	// gets them.
	board, err := a.boardCells(r, tenant.ID, game)
	if err != nil {
		serverError(w, "loading the trivia board", err)
		return
	}
	teams, cells, played, leader := a.gameCounts(r, game)
	writeJSON(w, map[string]any{
		"game":     a.gameToJSON(game, tenant.Slug, teams, cells, played, leader),
		"topics":   hist,
		"datasets": sets,
		"selected": ids,
		"state":    ProjectHost(snap),
		"cells":    board,
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
	if req.Settings == nil {
		clientError(w, r, http.StatusBadRequest, "no settings in the request")
		return
	}
	s := normaliseSettings(*req.Settings)
	if strings.TrimSpace(s.Title) == "" {
		s.Title = game.Title
	}
	if err := validateSettings(s); err != nil {
		clientError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := UpdateSettings(r.Context(), a.pool, tenant.ID, game.ID, s)
	if err != nil {
		serverError(w, "updating trivia settings", err)
		return
	}
	out := a.gameToJSON(updated, tenant.Slug, 0, 0, 0, "")
	// A board is a rendering of the shape settings, so a new shape means a
	// new board. Leaving the old one in place -- five columns of cells under
	// a setting that now says two -- was the confusing thing: the host typed
	// 2, the preview and the TV still showed 5, and nothing said why.
	if boardShapeChanged(game, updated) {
		if err := a.autoBuildBoard(r, tenant.ID, updated); err != nil {
			// The settings stand; the board does not. An empty board is an
			// honest state the setup page already knows how to explain, and
			// Start refuses it -- whereas a stale board would play.
			if clear := ReplaceBoard(r.Context(), a.pool, tenant.ID, game.ID, nil); clear != nil {
				serverError(w, "clearing trivia board", clear)
				return
			}
			out.BoardError = err.Error()
		}
	}
	writeJSON(w, out)
}

// boardShapeChanged reports whether a settings change needs the board
// redrawn: the columns, the rows, or what the cells are worth.
func boardShapeChanged(before, after *Game) bool {
	if before.BoardColumns != after.BoardColumns || before.BoardRows != after.BoardRows {
		return true
	}
	// Rounds are shape too. Leaving them out meant a host typed 2, the
	// settings said two rounds, and the board stayed one -- the same "I typed
	// it and nothing happened" this function exists to prevent.
	if boardRoundsOf(before) != boardRoundsOf(after) {
		return true
	}
	if len(before.CellValues) != len(after.CellValues) {
		return true
	}
	for i := range before.CellValues {
		if before.CellValues[i] != after.CellValues[i] {
			return true
		}
	}
	return false
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

// handleDeleteAllGames is the reset button. Member-level like the single
// delete: the person clearing last month's games is the person who ran
// them, and the console asks twice before it sends this.
func (a *App) handleDeleteAllGames(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	n, err := DeleteAllGames(r.Context(), a.pool, tenant.ID)
	if err != nil {
		serverError(w, "deleting all trivia games", err)
		return
	}
	slog.Info("trivia: deleted all games", "tenant_id", tenant.ID, "count", n)
	writeJSON(w, map[string]int{"deleted": n})
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

// callerID is the person behind the request, for created_by. A paired
// device is not a person, so a game it creates has no creator rather than
// a nil UUID pretending to be one.
func callerID(r *http.Request) *uuid.UUID {
	caller := auth.CallerFromContext(r.Context())
	if caller == nil || !caller.IsUser() {
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
