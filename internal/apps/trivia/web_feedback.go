package trivia

import (
	"encoding/json"
	"errors"
	"net/http"
)

// feedbackRequest is a table's rating, with an optional comment.
type feedbackRequest struct {
	Stars   int    `json:"stars"`
	Comment string `json:"comment"`
}

// handleFeedback posts a table's rating once the podium is up. Only a table
// that played can rate: a spectator has no cookie.
func (a *App) handleFeedback(w http.ResponseWriter, r *http.Request) {
	game, _, ok := a.resolveGame(w, r)
	if !ok {
		return
	}
	teamID, ok := a.requireTeam(w, r, game)
	if !ok {
		return
	}
	var req feedbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		clientError(w, r, http.StatusBadRequest, "invalid JSON")
		return
	}
	teams, err := ListTeams(r.Context(), a.pool, game.TenantID, game.ID)
	if err != nil {
		serverError(w, "listing trivia teams", err)
		return
	}
	var team *Team
	for i := range teams {
		if teams[i].ID == teamID {
			team = &teams[i]
		}
	}
	if team == nil {
		clientError(w, r, http.StatusUnauthorized, "join the game first")
		return
	}
	err = a.SendFeedback(r.Context(), game, team, req.Stars, req.Comment)
	switch {
	case errors.Is(err, ErrClosed):
		clientError(w, r, http.StatusConflict, err.Error())
		return
	case errors.Is(err, ErrBadRequest):
		clientError(w, r, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		serverError(w, "sending trivia feedback", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
