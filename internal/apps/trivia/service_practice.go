package trivia

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The practice round: one question, played from the lobby through every
// phase the real game has, so a room that has never played learns it on
// something that does not count.
//
// IT IS THE ORDINARY MACHINERY. A practice round is a round row with a flag
// on it, and it goes question -> cards -> chips -> scoring exactly like any
// other, scores and all -- the room watching its money move is half of what
// it is there to learn. What makes it practice is the exit: the host's "Next"
// from its scoring deletes the round (answers, cards, chips and scores
// cascade with it) and puts the game back in the lobby. Nothing downstream --
// standings, mentions, question numbering, who is in from which question --
// has to learn practice ever happened, because by the time any of them runs
// again it has not.
//
// Two things behave differently while it is up, both because the host is
// talking the room through it: it never closes early when every table is in
// (see everyoneIn), and the console puts Pause and the bigger time buttons
// front and centre.

// startPractice opens the warm-up question, preferring one the room has heard
// on an earlier night (see PracticeQuestion). Lobby only: once the board is up
// the room has already learned the game the hard way.
func (s *Service) startPractice(ctx context.Context, game *Game) error {
	if game.Phase != PhaseLobby && game.Phase != PhaseSetup {
		return fmt.Errorf("%w: practice is played from the lobby, before the game starts", ErrBadRequest)
	}
	datasets, err := GameDatasetIDs(ctx, s.pool, game.TenantID, game.ID)
	if err != nil {
		return err
	}
	q, err := PracticeQuestion(ctx, s.pool, game.TenantID, game.ID, datasets)
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("%w: there is no question left to practise on — every one is on the board", ErrBadRequest)
	}
	if err != nil {
		return err
	}
	// The first row's value, so writing the winning answer pays what it will
	// pay on the board and the room sees a real number land.
	points := 0
	if len(game.CellValues) > 0 {
		points = game.CellValues[0]
	}
	return s.startRound(ctx, game, roundSeed{QuestionID: q.ID, Points: points, IsPractice: true})
}

// inPractice reports whether the round on the wall is the warm-up.
func (s *Service) inPractice(ctx context.Context, game *Game) (bool, error) {
	if game.CurrentRoundID == nil {
		return false, nil
	}
	round, err := GetRound(ctx, s.pool, game.TenantID, *game.CurrentRoundID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return round.IsPractice, nil
}

// endPractice wipes the warm-up and takes the room back to the lobby, where
// Start begins the real game from nothing. The picker the practice scoring
// named goes too: Start draws the first one out of the hat, and a lobby
// naming a table would be the practice talking.
func (s *Service) endPractice(ctx context.Context, game *Game) error {
	if _, err := DeletePracticeRounds(ctx, s.pool, game.TenantID, game.ID); err != nil {
		return err
	}
	if err := SetPicker(ctx, s.pool, game.TenantID, game.ID, nil, ""); err != nil {
		return err
	}
	return s.moveTo(ctx, game, PhaseLobby, nil, nil)
}

// pauseClock stops or restarts the clock on the phase that is up. A click
// that lands after the phase moved on (or on a clock already in that state)
// is the usual conflict, not an error the host has to read.
func (s *Service) pauseClock(ctx context.Context, game *Game, pause bool) error {
	var ok bool
	var err error
	if pause {
		_, ok, err = PausePhase(ctx, s.pool, game.TenantID, game.ID, game.Phase)
	} else {
		_, ok, err = ResumePhase(ctx, s.pool, game.TenantID, game.ID, game.Phase)
	}
	if err != nil {
		return err
	}
	if !ok {
		return ErrPhaseConflict
	}
	return nil
}

// extend is the host's +15s (and practice's +1 min). It works on a paused
// clock too; see ExtendDeadline.
func (s *Service) extend(ctx context.Context, game *Game, seconds int) error {
	if seconds <= 0 || seconds > 300 {
		return fmt.Errorf("%w: extension must be 1-300 seconds", ErrBadRequest)
	}
	_, ok, err := ExtendDeadline(ctx, s.pool, game.TenantID, game.ID, game.Phase, time.Duration(seconds)*time.Second)
	if err != nil {
		return err
	}
	if !ok {
		return ErrPhaseConflict
	}
	return nil
}

// PracticeQuestion picks the warm-up: a question an EARLIER night has already
// asked, so practice never spends a fresh one the real game could use. Among
// those it takes the easiest -- the room is learning the mechanics, and a
// question everybody can get near makes the cards cluster, which is the
// betting decision at its clearest. Unrated questions sit mid-scale.
//
// A workspace on its first night has nothing asked yet, and then it falls
// back to the easiest fresh question rather than refusing the warm-up. The
// practice round is deleted afterwards, so that question stays fresh too.
func PracticeQuestion(ctx context.Context, pool *pgxpool.Pool, tenantID, gameID uuid.UUID,
	datasetIDs []uuid.UUID) (*Question, error) {
	q, err := scanQuestion(pool.QueryRow(ctx, `
		SELECT `+questionColumns+`
		  FROM app_trivia_questions q
		 WHERE q.tenant_id = $1
		   AND ($3::uuid[] IS NULL OR cardinality($3::uuid[]) = 0 OR q.dataset_id = ANY($3::uuid[]))
		   AND NOT EXISTS (SELECT 1 FROM app_trivia_board_cells c
		                    JOIN app_trivia_questions bq
		                      ON bq.id = c.question_id AND bq.tenant_id = c.tenant_id
		                   WHERE c.tenant_id = q.tenant_id AND c.game_id = $2
		                     AND bq.prompt_key = q.prompt_key)
		 ORDER BY NOT `+freshSQL("false", "$2")+` DESC,
		          COALESCE(q.difficulty, $4), q.last_used_at ASC NULLS FIRST, q.id
		 LIMIT 1`, tenantID, gameID, datasetIDs, unratedDifficulty))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("querying practice question: %w", err)
	}
	return q, nil
}

// DeletePracticeRounds removes the warm-up and everything hung off it, and
// clears the game's pointer if it was on it. Reports whether anything went.
func DeletePracticeRounds(ctx context.Context, pool *pgxpool.Pool, tenantID, gameID uuid.UUID) (bool, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("beginning practice cleanup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		UPDATE app_trivia_games g SET current_round_id = NULL
		 WHERE g.tenant_id = $1 AND g.id = $2
		   AND EXISTS (SELECT 1 FROM app_trivia_rounds r
		                WHERE r.tenant_id = g.tenant_id AND r.id = g.current_round_id
		                  AND r.is_practice)`, tenantID, gameID); err != nil {
		return false, fmt.Errorf("unpointing practice round: %w", err)
	}
	tag, err := tx.Exec(ctx,
		`DELETE FROM app_trivia_rounds WHERE tenant_id = $1 AND game_id = $2 AND is_practice`,
		tenantID, gameID)
	if err != nil {
		return false, fmt.Errorf("deleting practice rounds: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("committing practice cleanup: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}
