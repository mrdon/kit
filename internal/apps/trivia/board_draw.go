package trivia

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// drawBoard is the whole build, from bank to placed cells: load the questions
// this game is ALLOWED to ask, run the matching, and turn the result into
// rows.
//
// It sits here rather than in the HTTP handler because it is the one place
// the repeat rule turns into a board or into an error a host has to read, and
// both halves of that want testing without a request. The handler above it
// does routing and status codes; this does the game.
func drawBoard(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID,
	game *Game, topics []string, datasetIDs []uuid.UUID, seed int64) ([]BoardCell, error) {
	return drawRound(ctx, pool, tenantID, game, topics, datasetIDs, seed, 0, nil)
}

// drawRound is drawBoard for one round of a multi-round night.
//
// round stamps the cells and scales what they are worth -- every cell in a
// round is the same value, and the round doubles (see boardMultiplier). taken
// is the question ids already placed by EARLIER rounds of this same build,
// which the bank query cannot know about: it reads the database, and nothing
// has been written yet. Without it round two happily re-places round one's
// questions and the room is asked the same thing after the break.
func drawRound(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID,
	game *Game, topics []string, datasetIDs []uuid.UUID, seed int64,
	round int, taken map[uuid.UUID]bool) ([]BoardCell, error) {
	bank, err := QuestionsForTopics(ctx, pool, tenantID, topics, datasetIDs, freshnessOf(game))
	if err != nil {
		return nil, err
	}
	cands := make([]BoardCandidate, 0, len(bank))
	for _, q := range bank {
		if taken[q.ID] {
			continue
		}
		keys := make([]string, 0, len(q.Topics))
		for _, t := range q.Topics {
			keys = append(keys, t.Key)
		}
		cands = append(cands, BoardCandidate{QuestionID: q.ID.String(), TopicKeys: keys})
	}
	labels := topicLabels(bank)

	placed, err := BuildBoard(topics, game.BoardRows, game.CellValues, cands, seed)
	if err != nil {
		// The bank the matcher counted was already the fresh-only one, so a
		// shortfall has to SAY so. "topic has 1 question but the board needs
		// 2" sends a host hunting for questions that are sitting right there
		// in the set, already asked; "1 fresh question" tells them the truth,
		// and the console adds what to do about it.
		var se *ShortfallError
		if errors.As(err, &se) {
			se.Fresh = !game.RepeatQuestions
		}
		return nil, err
	}
	out := make([]BoardCell, 0, len(placed))
	for _, c := range placed {
		qid, err := uuid.Parse(c.QuestionID)
		if err != nil {
			return nil, fmt.Errorf("parsing question id: %w", err)
		}
		label := labels[c.Topic]
		if label == "" {
			label = c.Topic
		}
		out = append(out, BoardCell{
			RoundIndex: round,
			ColIndex:   c.ColIndex, RowIndex: c.RowIndex,
			Topic: label, Points: c.Points * boardMultiplier(round), QuestionID: qid,
		})
	}
	return out, nil
}

// topicLabels maps a folded key back to a display spelling, so the board
// header reads "Sports" rather than "sports".
func topicLabels(bank []Question) map[string]string {
	out := map[string]string{}
	for _, q := range bank {
		for _, t := range q.Topics {
			if _, seen := out[t.Key]; !seen {
				out[t.Key] = t.Label
			}
		}
	}
	return out
}
