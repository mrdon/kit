package trivia

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// closePhase ends one timed phase and arms the next. It is the single path
// all three closing layers take -- the host's button, the lazy sweep on every
// request, and the process ticker -- which is exactly what makes running them
// concurrently safe.
//
// The whole thing is one transaction whose FIRST statement is the guarded
// conditional UPDATE. If that updates zero rows somebody else already closed
// this phase, so the transaction rolls back having done nothing: no duplicate
// slots, no double scoring, no second publish. The work that follows (build
// the cards, or score the round) rides inside the same transaction as the
// row that authorised it.
//
// byTimer distinguishes the clock from a human: only the clock has to wait
// for the deadline to actually pass.
func (s *Service) closePhase(ctx context.Context, game *Game, from Phase, byTimer bool) error {
	to, deadline := nextPhase(game, from)
	if to == "" {
		return fmt.Errorf("%w: nothing to close from %s", ErrPhaseConflict, from)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning phase close: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, ok, err := AdvancePhase(ctx, tx, game.TenantID, game.ID, from, to, deadline, byTimer)
	if err != nil {
		return err
	}
	if !ok {
		return nil // somebody already advanced; do nothing, publish nothing
	}

	switch from {
	case PhaseQuestion:
		if err := s.buildReveal(ctx, tx, game); err != nil {
			return err
		}
	case PhaseBetting:
		if err := s.scoreRound(ctx, tx, game); err != nil {
			return err
		}
	case PhaseWager, PhaseSetup, PhaseLobby, PhaseBoard, PhaseIntermission, PhaseReveal, PhaseScoring, PhasePodium:
		// Wager -> question and reveal -> betting move the phase and nothing
		// else. The wager rows are already written and are read where they
		// are needed; there is no reveal to build and nothing to score. The
		// rest are not timed phases and never reach here.
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing phase close: %w", err)
	}
	return nil
}

// nextPhase is the state machine's forward edge, and the one place a phase's
// duration is chosen. Deadlines are computed here from the game's own
// settings and are absolute; nothing client-supplied reaches them.
func nextPhase(game *Game, from Phase) (Phase, *time.Time) {
	now := time.Now().UTC()
	arm := func(sec int) *time.Time {
		d := now.Add(time.Duration(sec) * time.Second)
		return &d
	}
	switch from {
	case PhaseWager:
		// The blind bet is over; NOW the room sees the question. From here on
		// the final is an ordinary round -- same answer clock, same reveal,
		// same betting -- which is why there is one extra phase and not four.
		return PhaseQuestion, arm(game.AnswerSeconds)
	case PhaseQuestion:
		// A deal beat of zero skips the reveal phase outright: the cards are
		// still built (closePhase does that on the way out of the question),
		// but the room sees them for the first time with betting already
		// open. Reading the cards and betting on them is the same act.
		if game.RevealSeconds <= 0 {
			return PhaseBetting, arm(game.BetSeconds)
		}
		return PhaseReveal, arm(game.RevealSeconds)
	case PhaseReveal:
		return PhaseBetting, arm(game.BetSeconds)
	case PhaseBetting:
		// Scoring waits on the host, so it carries no deadline. Clearing it
		// is also what takes the game back out of the sweeper's query.
		return PhaseScoring, nil
	case PhaseSetup, PhaseLobby, PhaseBoard, PhaseIntermission, PhaseScoring, PhasePodium:
		// Not timed: these wait on a human, which is correct -- the game
		// should pause between questions, hold through the break for as long
		// as the room needs, and stay on the podium forever.
		return "", nil
	}
	return "", nil
}

// buildReveal turns the room's answers into the cards it will bet on. Written
// once, inside the transaction that closed the answer phase, and never
// edited: the cards on the TV and the cards the phones bet on have to be the
// same objects.
func (s *Service) buildReveal(ctx context.Context, tx pgx.Tx, game *Game) error {
	if game.CurrentRoundID == nil {
		return nil
	}
	answers, err := ListAnswers(ctx, tx, game.TenantID, *game.CurrentRoundID)
	if err != nil {
		return err
	}
	in := make([]TeamAnswer, 0, len(answers))
	for _, a := range answers {
		in = append(in, TeamAnswer{TeamID: a.TeamID, Value: a.Value, Raw: a.Raw})
	}
	slots := BuildSlots(in)

	rows := make([]SlotRow, 0, len(slots))
	for _, sl := range slots {
		rows = append(rows, SlotRow{
			Position: sl.Position, Value: sl.Value, Label: sl.Label,
			Odds: sl.Odds, TeamIDs: sl.TeamIDs,
		})
	}
	return WriteSlots(ctx, tx, game.TenantID, *game.CurrentRoundID, rows)
}

// scoreRound runs the pure engine over the committed round and materialises
// the result. The leaderboard is then a SUM over those rows rather than a
// replay, so a later change to the engine cannot silently restate a game the
// room already watched.
func (s *Service) scoreRound(ctx context.Context, tx pgx.Tx, game *Game) error {
	if game.CurrentRoundID == nil {
		return nil
	}
	roundID := *game.CurrentRoundID
	round, err := GetRound(ctx, tx, game.TenantID, roundID)
	if err != nil {
		return err
	}
	in, slotRows, err := roundInput(ctx, tx, game, round)
	if err != nil {
		return err
	}
	result := ScoreRound(in)

	scores := make([]RoundScore, 0, len(result.Deltas))
	for teamID, d := range result.Deltas {
		scores = append(scores, RoundScore{
			RoundID: roundID, TeamID: teamID,
			BoardPoints: d.BoardPoints, BetDelta: d.BetDelta,
		})
	}
	if err := WriteRoundScores(ctx, tx, game.TenantID, scores); err != nil {
		return err
	}

	// The winning card, and the tables that wrote it. Both come off the same
	// row, because "which slot won" and "who picks next" must never be able
	// to disagree about which card that was.
	var winningID uuid.UUID
	var wroteWinner []uuid.UUID
	for _, r := range slotRows {
		if r.Position == result.WinningPos {
			winningID, wroteWinner = r.ID, r.TeamIDs
		}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE app_trivia_rounds SET winning_slot_id = $3, scored_at = now()
		  WHERE tenant_id = $1 AND id = $2`,
		game.TenantID, roundID, winningID); err != nil {
		return fmt.Errorf("recording winning slot: %w", err)
	}
	// Who picks the next category, in the SAME transaction that scored the
	// round -- this is the only moment the post-round scores exist, and the
	// rule needs them. See picker.go.
	return s.pickerAfterRound(ctx, tx, game, round.Ordinal+1, in.Banks, result.Deltas, wroteWinner)
}

// roundInput gathers what the pure engine needs: the cards, the chips against
// the positions they sit on, and every table's bank going in. Split out of
// scoreRound because assembling the input and materialising the result are
// two jobs, and together they were a function nobody could hold in their head.
func roundInput(ctx context.Context, tx pgx.Tx, game *Game, round *Round) (RoundInput, []SlotRow, error) {
	slotRows, err := ListSlots(ctx, tx, game.TenantID, round.ID)
	if err != nil {
		return RoundInput{}, nil, err
	}
	betRows, err := ListBets(ctx, tx, game.TenantID, round.ID)
	if err != nil {
		return RoundInput{}, nil, err
	}
	standings, err := Leaderboard(ctx, tx, game.TenantID, game.ID)
	if err != nil {
		return RoundInput{}, nil, err
	}

	slots := make([]Slot, 0, len(slotRows))
	posBySlot := map[uuid.UUID]int{}
	for _, r := range slotRows {
		posBySlot[r.ID] = r.Position
		slots = append(slots, Slot{
			Position: r.Position, Value: r.Value, Label: r.Label,
			TeamIDs: r.TeamIDs, Odds: r.Odds,
		})
	}
	bets := make([]RoundBet, 0, len(betRows))
	for _, b := range betRows {
		bets = append(bets, RoundBet{
			TeamID: b.TeamID, Amount: b.Amount,
			SlotPos: posBySlot[b.SlotID], TokenIdx: b.TokenIndex,
		})
	}
	banks := map[uuid.UUID]int{}
	for _, st := range standings {
		banks[st.TeamID] = st.Total
	}
	return RoundInput{
		// The round's own copy, not the bank's: the room is marked against
		// the answer it was actually asked.
		Correct:    round.AnswerValue,
		CellPoints: round.Points,
		Slots:      slots,
		Bets:       bets,
		IsFinal:    round.IsFinal,
		Banks:      banks,
	}, slotRows, nil
}

// getQuestionTx reads a question inside the scoring transaction. The answer
// has to come from the same snapshot of the world the slots did.
func getQuestionTx(ctx context.Context, tx pgx.Tx, tenantID, id uuid.UUID) (*Question, error) {
	q, err := scanQuestion(tx.QueryRow(ctx,
		`SELECT `+questionColumns+` FROM app_trivia_questions WHERE tenant_id = $1 AND id = $2`,
		tenantID, id))
	if err != nil {
		return nil, fmt.Errorf("querying question for scoring: %w", err)
	}
	return q, nil
}
