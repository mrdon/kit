package trivia

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
	ActionStart       Action = "start"
	ActionPickCell    Action = "pick_cell"
	ActionAsk         Action = "ask"
	ActionReveal      Action = "reveal"
	ActionOpenBetting Action = "open_betting"
	ActionScore       Action = "score"
	ActionNext        Action = "next"
	ActionResume      Action = "resume"
	ActionAddRound    Action = "add_round"
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
	case ActionStart:
		// A game cannot start without a board. Starting into an empty board
		// puts the room in front of a screen with nothing to pick, and the
		// only way out is for the host to end the game — so it is refused
		// here with a message that says what to do instead.
		cells, err := ListBoardCells(ctx, s.pool, game.TenantID, game.ID)
		if err != nil {
			return err
		}
		if len(cells) == 0 {
			return fmt.Errorf("%w: this game has no board yet — add some questions and build one on the setup page", ErrBadRequest)
		}
		if err := s.drawFirstPicker(ctx, game); err != nil {
			return err
		}
		return s.moveTo(ctx, game, PhaseBoard, nil, nil)
	case ActionPickCell:
		return s.openCell(ctx, game, req.CellID)
	case ActionAsk:
		// The wager clock cut short by a human: every table has committed (or
		// the host has decided the stragglers have had long enough) and the
		// question goes up.
		return s.closePhase(ctx, game, PhaseWager, false)
	case ActionReveal:
		return s.closePhase(ctx, game, PhaseQuestion, false)
	case ActionOpenBetting:
		return s.closePhase(ctx, game, PhaseReveal, false)
	case ActionScore:
		return s.closePhase(ctx, game, PhaseBetting, false)
	case ActionNext:
		return s.afterScoring(ctx, game)
	case ActionAddRound:
		// Legal from the break and from an emptied board, which are the two
		// moments a host is actually asked "shall we do another?".
		return s.AddBoardRound(ctx, game.TenantID, game.ID)
	case ActionResume:
		// The break is over. Nothing was on a clock, so there is no phase to
		// close -- the host says the room is back and the next board opens.
		return s.moveTo(ctx, game, PhaseBoard, nil, nil)
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

// drawFirstPicker pulls the first category's picker out of the hat, before
// the game moves onto the board.
//
// Written BEFORE the move rather than after it so the very first board frame
// the TV ever sees already names a table -- the wheel has something to land
// on in the same frame the board arrives in, and there is no half-second
// where the wall says nothing. The move bumps the state version for both.
//
// An empty room draws nobody and that is fine: a game started with no tables
// has no picker, every surface renders the absence, and the next scored round
// will fill it in.
func (s *Service) drawFirstPicker(ctx context.Context, game *Game) error {
	teams, err := ListTeams(ctx, s.pool, game.TenantID, game.ID)
	if err != nil {
		return err
	}
	teamID, ok := DrawPicker(teams)
	if !ok {
		return nil
	}
	return SetPicker(ctx, s.pool, game.TenantID, game.ID, &teamID, PickerDrawn)
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
	return s.startRound(ctx, game, roundSeed{
		QuestionID: cell.QuestionID, CellID: &cell.ID,
		Points: cell.Points, Topic: cell.Topic,
	})
}

// roundSeed is everything a round needs to exist, gathered by whichever of
// openCell/openFinal is opening it. Two call sites and six values is exactly
// where a parameter list stops being readable.
type roundSeed struct {
	QuestionID uuid.UUID
	CellID     *uuid.UUID
	Points     int
	// Topic is the column a board round came from. Empty for a final, whose
	// category is looked up from the question inside the transaction.
	Topic   string
	IsFinal bool
}

// openFinal starts the one round that stakes a team's own money.
//
// It opens into PhaseWager rather than PhaseQuestion: the room sees the
// category and a clock, commits an amount, and only then is the question read
// out. The partial unique index on (tenant_id, game_id) WHERE is_final means a
// second "final" click cannot open a second one.
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
		q, err := LeastUsedQuestion(ctx, s.pool, game.TenantID, datasets, freshnessOf(game))
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
	return s.startRound(ctx, game, roundSeed{QuestionID: qID, Points: points, IsFinal: true})
}

// startRound writes the round, marks its cell played, and arms the opening
// clock -- all in one transaction, so a game can never be pointed at a round
// that does not exist.
func (s *Service) startRound(ctx context.Context, game *Game, seed roundSeed) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning round: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	roundID, err := insertRoundTx(ctx, tx, game, seed)
	if err != nil {
		return err
	}
	if seed.CellID != nil {
		if _, err := tx.Exec(ctx,
			`UPDATE app_trivia_board_cells SET played_at = now()
			  WHERE tenant_id = $1 AND id = $2 AND played_at IS NULL`,
			game.TenantID, *seed.CellID); err != nil {
			return fmt.Errorf("marking cell played: %w", err)
		}
	}
	phase, deadline := openingPhase(game, seed.IsFinal)
	if _, err := tx.Exec(ctx, `
		UPDATE app_trivia_games
		   SET phase = $3, phase_deadline = $4, current_round_id = $5,
		       state_version = state_version + 1, updated_at = now()
		 WHERE tenant_id = $1 AND id = $2`,
		game.TenantID, game.ID, phase, deadline, roundID); err != nil {
		return fmt.Errorf("arming %s phase: %w", phase, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing round: %w", err)
	}
	return nil
}

// openingPhase is where a round lands the moment it opens, and how long it
// has there.
//
// A board round goes straight to the question. The final stops at `wager`
// first, which is the ONE structural difference between the two: the room
// commits an amount against a category, and the prompt does not exist on any
// public surface until that clock runs out.
func openingPhase(game *Game, isFinal bool) (Phase, time.Time) {
	now := time.Now().UTC()
	if isFinal {
		return PhaseWager, now.Add(time.Duration(game.WagerSeconds) * time.Second)
	}
	return PhaseQuestion, now.Add(time.Duration(game.AnswerSeconds) * time.Second)
}

// insertRoundTx copies the question onto a new round.
//
// From here the round is self-contained: the recap, the scoring, the wager
// screen and the TV all read this copy, so a re-upload or a deleted dataset
// cannot change what the room was asked, what it was marked against, or what
// category it was told it was betting on.
//
// prompt_key travels with the copy for one more reason: this row IS the
// record that the question has been asked, and next week's board asks it
// about a key, not an id. See Freshness.
func insertRoundTx(ctx context.Context, tx pgx.Tx, game *Game, seed roundSeed) (uuid.UUID, error) {
	var ordinal int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(max(ordinal), 0) + 1 FROM app_trivia_rounds WHERE tenant_id = $1 AND game_id = $2`,
		game.TenantID, game.ID).Scan(&ordinal); err != nil {
		return uuid.Nil, fmt.Errorf("computing round ordinal: %w", err)
	}
	question, err := getQuestionTx(ctx, tx, game.TenantID, seed.QuestionID)
	if err != nil {
		return uuid.Nil, err
	}
	topic := seed.Topic
	if topic == "" {
		// A final has no board column to inherit, so its category is the
		// question's own first topic. Blank is survivable -- the wager screen
		// simply has no category line -- so a question filed under nothing
		// does not stop the final opening.
		if topic, err = firstTopicTx(ctx, tx, game.TenantID, seed.QuestionID); err != nil {
			return uuid.Nil, err
		}
	}
	var roundID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO app_trivia_rounds
		    (tenant_id, game_id, cell_id, question_id, prompt, prompt_key,
		     answer_value, answer_text, topic, is_final, ordinal, points)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`,
		game.TenantID, game.ID, seed.CellID, seed.QuestionID,
		question.Prompt, question.PromptKey, question.AnswerValue, question.AnswerText,
		topic, seed.IsFinal, ordinal, seed.Points).Scan(&roundID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("inserting round: %w", err)
	}
	return roundID, nil
}

// firstTopicTx reads a question's category, alphabetically first when it
// carries several. Alphabetical rather than "whichever the index returns"
// because the room is shown this string and a final reopened from a backup
// should say the same word it said the first time.
func firstTopicTx(ctx context.Context, tx pgx.Tx, tenantID, questionID uuid.UUID) (string, error) {
	var topic string
	err := tx.QueryRow(ctx, `
		SELECT topic FROM app_trivia_question_topics
		 WHERE tenant_id = $1 AND question_id = $2
		 ORDER BY topic_key LIMIT 1`, tenantID, questionID).Scan(&topic)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("reading question topic: %w", err)
	}
	return topic, nil
}

// afterScoring is the host's "next": back to the board, or -- when the board
// is empty -- to the final if this game has one and to the podium if not.
func (s *Service) afterScoring(ctx context.Context, game *Game) error {
	cells, err := ListBoardCells(ctx, s.pool, game.TenantID, game.ID)
	if err != nil {
		return err
	}
	if len(cells) == 0 {
		return s.moveTo(ctx, game, PhaseBoard, nil, nil)
	}
	round, total := CurrentBoardRound(cells), BoardRoundCount(cells)

	if round < total {
		// Crossing into a round nobody has touched yet is the seam in the
		// night, and it is the only signal needed: the round just played is
		// exhausted (or CurrentBoardRound would still be on it) and the next
		// one has not started. Round 0 is excluded because the top of the
		// night is not a break.
		if round > 0 && !anyPlayedInRound(cells, round) {
			return s.moveTo(ctx, game, PhaseIntermission, nil, nil)
		}
		return s.moveTo(ctx, game, PhaseBoard, nil, nil)
	}
	if game.FinalWager {
		rounds, err := ListRounds(ctx, s.pool, game.TenantID, game.ID)
		if err != nil {
			return err
		}
		for _, r := range rounds {
			if r.IsFinal {
				// The final has been played; that really was the end.
				return s.moveTo(ctx, game, PhasePodium, nil, nil)
			}
		}
	}
	// Wait on the emptied board. THE GAME DOES NOT END ITSELF.
	//
	// With a final this was always the behaviour -- the host presses "Final
	// question" when the room is ready. Without one the board used to go
	// straight to the podium, which took the decision off the host at the one
	// moment they most want it: the room is enjoying itself, the board is
	// spent, and the honest question is "another round, or shall we call it?"
	// Ending automatically answered it for them, and it also left the
	// console's own "Go to the podium" button unreachable -- the UI had
	// expected this wait all along.
	//
	// The podium is one click away (ActionFinish, legal from any phase) and
	// the host is standing in the room; nothing is lost by asking.
	return s.moveTo(ctx, game, PhaseBoard, nil, nil)
}

// anyPlayedInRound reports whether a round has been started at all.
func anyPlayedInRound(cells []BoardCell, round int) bool {
	for _, c := range cells {
		if c.RoundIndex == round && c.PlayedAt != nil {
			return true
		}
	}
	return false
}
