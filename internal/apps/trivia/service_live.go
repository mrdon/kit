package trivia

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ErrPhaseConflict means the host clicked from a phase the game has already
// left. Every host click carries the phase it was made from, so a
// double-clicked "Next" is refused rather than silently skipping a question.
var ErrPhaseConflict = errors.New("trivia: the game has already moved on")

// ErrClosed means the phase that would accept this submission has ended.
var ErrClosed = errors.New("trivia: that phase has closed")

// ErrBadRequest covers input the caller can fix.
var ErrBadRequest = errors.New("trivia: invalid request")

// Action names one host click. There is ONE host endpoint carrying an action
// and a from-phase rather than eight endpoints, because every host click is
// the same shape: a guarded transition needing the same conflict check.
type Action string

// The host's vocabulary.
const (
	ActionOpenLobby   Action = "open_lobby"
	ActionStart       Action = "start"
	ActionPickCell    Action = "pick_cell"
	ActionReveal      Action = "reveal"
	ActionOpenBetting Action = "open_betting"
	ActionScore       Action = "score"
	ActionNext        Action = "next"
	ActionFinal       Action = "final"
	ActionExtend      Action = "extend"
	ActionFinish      Action = "finish"
)

// ActionRequest is the one host endpoint's body.
type ActionRequest struct {
	Action    Action     `json:"action"`
	FromPhase Phase      `json:"from_phase"`
	CellID    *uuid.UUID `json:"cell_id,omitempty"`
	Seconds   int        `json:"seconds,omitempty"`
	// QuestionID picks the final's question explicitly; nil draws the
	// least-recently-used one from the bank.
	QuestionID *uuid.UUID `json:"question_id,omitempty"`
}

// Do performs one host action and publishes the result.
//
// Every mutation commits before anything is fanned out: publishing from
// inside a transaction would mean a rollback had already been broadcast to
// the room.
func (s *Service) Do(ctx context.Context, tenantID, gameID uuid.UUID, req ActionRequest) (*Snapshot, error) {
	// Heal first. A host whose laptop was asleep across a deadline is
	// clicking against a phase the database has already left.
	if err := s.SweepDue(ctx, tenantID, gameID); err != nil {
		return nil, err
	}
	game, err := GetGame(ctx, s.pool, tenantID, gameID)
	if err != nil {
		return nil, err
	}
	if req.Action != ActionFinish && req.FromPhase != "" && game.Phase != req.FromPhase {
		return nil, ErrPhaseConflict
	}

	if err := s.applyAction(ctx, game, req); err != nil {
		return nil, err
	}
	snap, err := s.Snapshot(ctx, tenantID, gameID)
	if err != nil {
		return nil, err
	}
	s.broker.Publish(gameID, snap)
	if s.relay != nil {
		s.relay.publish(ctx, snap)
	}
	return snap, nil
}

// applyAction dispatches one action. Kept separate from Do so the publish and
// the conflict check are written once.
func (s *Service) applyAction(ctx context.Context, game *Game, req ActionRequest) error {
	switch req.Action {
	case ActionOpenLobby:
		return s.moveTo(ctx, game, PhaseLobby, nil, game.CurrentRoundID)
	case ActionStart:
		return s.moveTo(ctx, game, PhaseBoard, nil, nil)
	case ActionPickCell:
		return s.openCell(ctx, game, req.CellID)
	case ActionReveal:
		return s.closePhase(ctx, game, PhaseQuestion, false)
	case ActionOpenBetting:
		return s.closePhase(ctx, game, PhaseReveal, false)
	case ActionScore:
		return s.closePhase(ctx, game, PhaseBetting, false)
	case ActionNext:
		return s.afterScoring(ctx, game)
	case ActionFinal:
		return s.openFinal(ctx, game, req.QuestionID)
	case ActionExtend:
		return s.extend(ctx, game, req.Seconds)
	case ActionFinish:
		// Legal from any phase and jumps straight to the podium. A quiz
		// night that has to end because the kitchen is closing should not
		// require playing out the board.
		return s.moveTo(ctx, game, PhasePodium, nil, game.CurrentRoundID)
	default:
		return fmt.Errorf("%w: unknown action %q", ErrBadRequest, req.Action)
	}
}

func (s *Service) moveTo(ctx context.Context, game *Game, to Phase, deadline *time.Time, roundID *uuid.UUID) error {
	_, err := SetPhaseUnconditional(ctx, s.pool, game.TenantID, game.ID, to, deadline, roundID)
	return err
}

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

// openCell starts a round on a board cell. The unique index on
// (tenant_id, cell_id) is what makes a double-clicked tile open one round
// rather than two -- a handler check would let two racing clicks both pass.
func (s *Service) openCell(ctx context.Context, game *Game, cellID *uuid.UUID) error {
	if cellID == nil {
		return fmt.Errorf("%w: pick_cell needs a cell_id", ErrBadRequest)
	}
	cells, err := ListBoardCells(ctx, s.pool, game.TenantID, game.ID)
	if err != nil {
		return err
	}
	var cell *BoardCell
	for i := range cells {
		if cells[i].ID == *cellID {
			cell = &cells[i]
		}
	}
	if cell == nil {
		return ErrNotFound
	}
	if cell.PlayedAt != nil {
		return fmt.Errorf("%w: that cell has already been played", ErrPhaseConflict)
	}
	return s.startRound(ctx, game, cell.QuestionID, &cell.ID, cell.Points, false)
}

// openFinal starts the one round that stakes a team's own money. It re-enters
// PhaseQuestion with is_final set: no new phase, no new table, and the
// partial unique index on (tenant_id, game_id) WHERE is_final means a second
// "final" click cannot open a second one.
func (s *Service) openFinal(ctx context.Context, game *Game, questionID *uuid.UUID) error {
	if !game.FinalWager {
		// With the final switched off this action does not exist. The host
		// console never offers it; refusing here is what makes "off" the
		// genuine absence of the mechanic rather than a hidden button.
		return fmt.Errorf("%w: this game has the final wager switched off", ErrBadRequest)
	}
	var qID uuid.UUID
	if questionID != nil {
		qID = *questionID
	} else {
		// The final draws from the same datasets the board did, so a themed
		// night does not end on a question from some other pack.
		datasets, err := GameDatasetIDs(ctx, s.pool, game.TenantID, game.ID)
		if err != nil {
			return err
		}
		q, err := LeastUsedQuestion(ctx, s.pool, game.TenantID, game.ID, datasets)
		if err != nil {
			return err
		}
		qID = q.ID
	}
	// The final's board points are the dearest cell value, so writing the
	// winning answer at the end is worth what it was worth all night.
	points := 0
	if len(game.CellValues) > 0 {
		points = game.CellValues[len(game.CellValues)-1]
	}
	return s.startRound(ctx, game, qID, nil, points, true)
}

// startRound writes the round, marks its cell played, and arms the answer
// clock -- all in one transaction, so a game can never be pointed at a round
// that does not exist.
func (s *Service) startRound(ctx context.Context, game *Game, questionID uuid.UUID, cellID *uuid.UUID, points int, isFinal bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning round: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var ordinal int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(max(ordinal), 0) + 1 FROM app_trivia_rounds WHERE tenant_id = $1 AND game_id = $2`,
		game.TenantID, game.ID).Scan(&ordinal); err != nil {
		return fmt.Errorf("computing round ordinal: %w", err)
	}

	// Copy the question onto the round as it opens. From here the round is
	// self-contained: the recap, the scoring and the TV all read this copy,
	// so a re-upload or a deleted dataset cannot change what the room was
	// asked or what it was marked against.
	question, err := getQuestionTx(ctx, tx, game.TenantID, questionID)
	if err != nil {
		return err
	}

	var roundID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO app_trivia_rounds
		    (tenant_id, game_id, cell_id, question_id, prompt, answer_value, answer_text,
		     is_final, ordinal, points)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`,
		game.TenantID, game.ID, cellID, questionID,
		question.Prompt, question.AnswerValue, question.AnswerText,
		isFinal, ordinal, points).Scan(&roundID)
	if err != nil {
		return fmt.Errorf("inserting round: %w", err)
	}
	if cellID != nil {
		if _, err := tx.Exec(ctx,
			`UPDATE app_trivia_board_cells SET played_at = now()
			  WHERE tenant_id = $1 AND id = $2 AND played_at IS NULL`,
			game.TenantID, *cellID); err != nil {
			return fmt.Errorf("marking cell played: %w", err)
		}
	}
	deadline := time.Now().UTC().Add(time.Duration(game.AnswerSeconds) * time.Second)
	if _, err := tx.Exec(ctx, `
		UPDATE app_trivia_games
		   SET phase = $3, phase_deadline = $4, current_round_id = $5,
		       state_version = state_version + 1, updated_at = now()
		 WHERE tenant_id = $1 AND id = $2`,
		game.TenantID, game.ID, PhaseQuestion, deadline, roundID); err != nil {
		return fmt.Errorf("arming question phase: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing round: %w", err)
	}
	return nil
}

// afterScoring is the host's "next": back to the board, or -- when the board
// is empty -- to the final if this game has one and to the podium if not.
func (s *Service) afterScoring(ctx context.Context, game *Game) error {
	cells, err := ListBoardCells(ctx, s.pool, game.TenantID, game.ID)
	if err != nil {
		return err
	}
	played := 0
	for _, c := range cells {
		if c.PlayedAt != nil {
			played++
		}
	}
	boardEmpty := len(cells) > 0 && played == len(cells)

	if !boardEmpty {
		return s.moveTo(ctx, game, PhaseBoard, nil, nil)
	}
	if !game.FinalWager {
		return s.moveTo(ctx, game, PhasePodium, nil, nil)
	}
	rounds, err := ListRounds(ctx, s.pool, game.TenantID, game.ID)
	if err != nil {
		return err
	}
	for _, r := range rounds {
		if r.IsFinal {
			// The final has already been played; that was the end.
			return s.moveTo(ctx, game, PhasePodium, nil, nil)
		}
	}
	// Wait on the board with nothing left to pick: the host presses "Final
	// question" when the room is ready for it.
	return s.moveTo(ctx, game, PhaseBoard, nil, nil)
}
