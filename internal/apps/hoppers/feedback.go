package hoppers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/hoppers/engine"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/livegame"
)

// ErrNotOver is a rating sent before the night has its final standings.
var ErrNotOver = errors.New("hoppers: the night is not over yet")

// SendFeedback posts a phone's end-of-night rating to the channel the
// workspace picked for Hoppers. A rating that does not reach Slack (no
// channel, or Slack failing) is logged in full and otherwise dropped: the
// phone can do nothing about either, and the end of the night is not the
// moment to hand it an error.
func (a *App) SendFeedback(ctx context.Context, game *Game, player *Player, stars int, comment string) error {
	if game.Phase != string(engine.PhaseFinal) {
		return ErrNotOver
	}
	comment, err := livegame.ValidateFeedback(stars, comment)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrBadRequest, strings.TrimPrefix(err.Error(), livegame.ErrBadFeedback.Error()+": "))
	}
	if a.feedback == nil {
		return nil
	}
	title := game.Title
	if title == "" {
		title = "Hoppers"
	}
	text := livegame.FeedbackText(title, player.Name, stars, comment)
	err = a.feedback.Post(ctx, game.TenantID, text)
	switch {
	case errors.Is(err, livegame.ErrFeedbackOff):
		slog.Info("hoppers feedback not posted, no channel picked", "game_id", game.ID, "player_id", player.ID, "text", text)
	case err != nil:
		slog.Warn("posting hoppers feedback to slack", "game_id", game.ID, "player_id", player.ID, "text", text, "error", err)
	}
	return nil
}

type feedbackRequest struct {
	Stars   int    `json:"stars"`
	Comment string `json:"comment"`
}

// handleFeedback is a phone's rating, once the night is over.
func (a *App) handleFeedback(w http.ResponseWriter, r *http.Request) {
	game, _, ok := a.resolveGame(w, r)
	if !ok {
		return
	}
	id := a.callerPlayer(r, game)
	if id == uuid.Nil {
		clientError(w, r, http.StatusUnauthorized, "join the game first")
		return
	}
	var req feedbackRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&req); err != nil {
		clientError(w, r, http.StatusBadRequest, "invalid JSON")
		return
	}
	player, err := GetPlayer(r.Context(), a.pool, game.TenantID, game.ID, id)
	if err != nil {
		clientError(w, r, http.StatusUnauthorized, "join the game first")
		return
	}
	err = a.SendFeedback(r.Context(), game, player, req.Stars, req.Comment)
	switch {
	case errors.Is(err, ErrNotOver):
		clientError(w, r, http.StatusConflict, err.Error())
		return
	case errors.Is(err, ErrBadRequest):
		clientError(w, r, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		serverError(w, "sending hoppers feedback", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleGetFeedbackChannel is the admin's "where ratings go" panel.
func (a *App) handleGetFeedbackChannel(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	current, err := GetFeedbackChannel(r.Context(), a.pool, caller.TenantID)
	if err != nil {
		serverError(w, "loading hoppers feedback channel", err)
		return
	}
	writeJSON(w, livegame.NewChannelPayload(r.Context(), a.pool, a.enc, caller.TenantID, current))
}

// handleSaveFeedbackChannel stores the admin's pick, refusing a channel Kit
// is not in with a sentence saying how to fix it.
func (a *App) handleSaveFeedbackChannel(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	var body struct {
		ChannelID string `json:"channel_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&body); err != nil {
		clientError(w, r, http.StatusBadRequest, "invalid JSON")
		return
	}
	choice := livegame.FeedbackChannel{}
	if strings.TrimSpace(body.ChannelID) != "" {
		options, listErr := livegame.ChannelOptions(r.Context(), a.pool, a.enc, caller.TenantID)
		if listErr != "" {
			clientError(w, r, http.StatusBadGateway, listErr)
			return
		}
		picked, err := livegame.PickChannel(options, body.ChannelID)
		if err != nil {
			clientError(w, r, http.StatusBadRequest, err.Error())
			return
		}
		choice = picked
	}
	if err := SaveFeedbackChannel(r.Context(), a.pool, caller.TenantID, choice); err != nil {
		serverError(w, "saving hoppers feedback channel", err)
		return
	}
	writeJSON(w, livegame.NewChannelPayload(r.Context(), a.pool, a.enc, caller.TenantID, choice))
}

// handleResults is the history of finished nights.
func (a *App) handleResults(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	results, err := ListResults(r.Context(), a.pool, tenant.ID, 50)
	if err != nil {
		serverError(w, "listing hoppers results", err)
		return
	}
	writeJSON(w, map[string]any{"results": results})
}
