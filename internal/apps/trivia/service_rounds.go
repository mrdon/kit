package trivia

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/google/uuid"
)

// Adding a board round to a night already in progress.
//
// THE DECISION IS NOT KNOWABLE AT SETUP. Whether the room wants another half
// hour depends on the room: how full it is, whether the kitchen is still on,
// whether it is raining. A host who had to commit at seven o'clock was
// guessing, and the only way to be wrong in the fun direction was to
// over-commit and watch people leave mid-board.
//
// It is cheap because of how rounds were built: cells carry a round index,
// the round in play is DERIVED from which of them are unplayed, and the
// multiplier comes off the index. So a new round is an append -- nothing to
// reconcile, no state to move, and the phase machine notices on its own.

// AddBoardRound appends one more board with categories this night has not
// used, at the next round index and the next multiplier.
func (s *Service) AddBoardRound(ctx context.Context, tenantID, gameID uuid.UUID) error {
	game, err := GetGame(ctx, s.pool, tenantID, gameID)
	if err != nil {
		return err
	}
	// Before the doors open the setting is the right tool -- it rebuilds the
	// whole night coherently. This is for a night already running.
	if game.Phase == PhaseSetup || game.Phase == PhaseAwards || game.Phase == PhasePodium {
		return fmt.Errorf("%w: rounds are added while the game is running, not before or after", ErrBadRequest)
	}
	cells, err := ListBoardCells(ctx, s.pool, tenantID, gameID)
	if err != nil {
		return err
	}
	round := BoardRoundCount(cells)
	if round >= maxBoardRounds {
		return fmt.Errorf("%w: a night runs at most %d board rounds", ErrBadRequest, maxBoardRounds)
	}

	// Categories this night has already used are out. Coming back from the
	// break to the same five columns reads as the same board again, and the
	// room says so.
	usedTopic := map[string]bool{}
	usedQuestion := map[uuid.UUID]bool{}
	for _, c := range cells {
		usedTopic[cellTopicKey(c)] = true
		usedQuestion[c.QuestionID] = true
	}

	datasetIDs, err := GameDatasetIDs(ctx, s.pool, tenantID, gameID)
	if err != nil {
		return err
	}
	hist, err := TopicHistogram(ctx, s.pool, tenantID, datasetIDs, freshnessOf(game))
	if err != nil {
		return err
	}
	fresh := make([]TopicCount, 0, len(hist))
	for _, tc := range hist {
		if !usedTopic[tc.Key] {
			fresh = append(fresh, tc)
		}
	}
	topics := PickTopics(fresh, game.BoardColumns, game.BoardRows, time.Now().UnixNano())
	if len(topics) < game.BoardColumns {
		kind := "fresh question"
		if game.RepeatQuestions {
			kind = "question"
		}
		if game.BoardRows != 1 {
			kind += "s"
		}
		return fmt.Errorf("%w: only %d unused categor%s have %d %s left — not enough for another round",
			ErrBadRequest, len(topics), plural2(len(topics)), game.BoardRows, kind)
	}

	seed := rand.Int63() //nolint:gosec // board variety, not secrecy
	next, err := drawRound(ctx, s.pool, tenantID, game, topics, datasetIDs, seed, round, usedQuestion)
	if err != nil {
		return err
	}
	if err := AppendBoardCells(ctx, s.pool, tenantID, gameID, next); err != nil {
		return err
	}
	// A night sitting on a SPENT board goes through the break on its way into
	// the new one, rather than swapping the grid out underneath the room.
	//
	// The break screen is the only surface in the product that explains the
	// scaling -- it names the next round's cell and chip values -- so skipping
	// it is exactly the moment the room would be given new money with no
	// announcement. From the intermission this is already where we are.
	if game.Phase == PhaseBoard {
		return s.moveTo(ctx, game, PhaseIntermission, nil, nil)
	}
	s.publish(ctx, tenantID, gameID)
	return nil
}

// plural2 is the "y"/"ies" ending, which reads badly inlined in a format.
func plural2(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
