package trivia

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/mrdon/kit/internal/livegame"
)

// SendFeedback posts a table's rating to the workspace's chosen channel. It
// is not stored: Slack is the record.
//
// A rating that does not reach Slack, for want of a channel or because Slack
// failed, is logged in full and otherwise dropped. The table is never told:
// it can do nothing about either, and the end of the night is not the moment
// to hand it an error.
func (a *App) SendFeedback(ctx context.Context, game *Game, team *Team, stars int, comment string) error {
	// BOTH ending phases. The mentions are where the asking should happen --
	// the host is reading a list out and every table has nothing to do but
	// listen, which is the best moment of the night to be handed five stars.
	// The podium keeps it for whoever had not got round to it.
	//
	// Podium-only cost a real night its ratings: the game stopped on the
	// mentions, the rating lived one screen further on, and six tables were
	// never asked.
	if game.Phase != PhasePodium {
		return fmt.Errorf("%w: the game is not over yet", ErrClosed)
	}
	comment, err := livegame.ValidateFeedback(stars, comment)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrBadRequest, strings.TrimPrefix(err.Error(), livegame.ErrBadFeedback.Error()+": "))
	}
	if a.feedback == nil {
		return nil
	}
	text := livegame.FeedbackText(gameLabel(game), team.Name, stars, comment)
	err = a.feedback.Post(ctx, game.TenantID, text)
	switch {
	case errors.Is(err, livegame.ErrFeedbackOff):
		slog.Info("trivia feedback not posted, no channel picked", "game_id", game.ID, "team_id", team.ID, "text", text)
	case err != nil:
		slog.Warn("posting trivia feedback to slack", "game_id", game.ID, "team_id", team.ID, "text", text, "error", err)
	}
	return nil
}

func gameLabel(game *Game) string {
	if game.Title != "" {
		return game.Title
	}
	return game.Name
}
