package trivia

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// playOneRound drives a game from the board through a scored round, so the
// tests that care about a specific rule do not each re-type the whole flow.
func (f *fixture) playOneRound(game *Game, answers map[uuid.UUID]string) *Game {
	f.t.Helper()
	snap, err := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	if err != nil {
		f.t.Fatalf("Snapshot: %v", err)
	}
	var cellID uuid.UUID
	for _, c := range snap.Board {
		if !c.Played {
			cellID = c.ID
			break
		}
	}
	if cellID == uuid.Nil {
		f.t.Fatal("no unplayed cell left")
	}
	f.do(game.ID, ActionRequest{Action: ActionPickCell, FromPhase: PhaseBoard, CellID: &cellID})
	for teamID, raw := range answers {
		if err := f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, teamID, raw, nil); err != nil {
			f.t.Fatalf("SubmitAnswer: %v", err)
		}
	}
	g := f.reload(game.ID)
	if g.Phase == PhaseQuestion {
		f.do(game.ID, ActionRequest{Action: ActionReveal, FromPhase: PhaseQuestion})
	}
	f.do(game.ID, ActionRequest{Action: ActionOpenBetting, FromPhase: PhaseReveal})
	f.do(game.ID, ActionRequest{Action: ActionScore, FromPhase: PhaseBetting})
	return f.reload(game.ID)
}

// The full flow, once, against a real database: pick a cell, everyone
// answers, reveal, bet, score, and the leaderboard reflects it.
func TestRoundFlowEndToEnd(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())
	f.do(game.ID, ActionRequest{Action: ActionOpenLobby, FromPhase: PhaseSetup})
	a := f.join(game.ID, "Bar Flies")
	b := f.join(game.ID, "Quiz Khalifa")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})

	snap, err := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Board) != 10 {
		t.Fatalf("board has %d cells, want 10", len(snap.Board))
	}
	cellID := snap.Board[0].ID
	f.do(game.ID, ActionRequest{Action: ActionPickCell, FromPhase: PhaseBoard, CellID: &cellID})

	snap, _ = f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	if snap.Phase != PhaseQuestion || snap.Round == nil {
		t.Fatalf("phase = %s round = %v", snap.Phase, snap.Round)
	}
	if snap.Deadline == nil {
		t.Fatal("the answer clock was not armed when the question opened")
	}
	correct := snap.Round.CorrectValue

	// One team is exactly right, the other overshoots.
	if err := f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, a.ID, FormatValue(correct), nil); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, b.ID, FormatValue(correct+50), nil); err != nil {
		t.Fatal(err)
	}

	// Everyone answered, so the phase closed early without anyone clicking.
	g := f.reload(game.ID)
	if g.Phase != PhaseReveal {
		t.Fatalf("phase = %s after every eligible team answered, want reveal", g.Phase)
	}

	snap, _ = f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	if len(snap.Slots) != 3 {
		t.Fatalf("got %d cards, want 3 (pseudo + two distinct answers)", len(snap.Slots))
	}
	if snap.Slots[0].Position != 0 || snap.Slots[0].Value != nil {
		t.Fatalf("leftmost card = %+v, want the pseudo-slot", snap.Slots[0])
	}

	f.do(game.ID, ActionRequest{Action: ActionOpenBetting, FromPhase: PhaseReveal})
	snap, _ = f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)

	// B backs A's correct card with the $200 chip and the pseudo-slot with
	// the $100 -- two different answers, as the rule requires.
	var winningSlot uuid.UUID
	for _, sl := range snap.Slots {
		if sl.Value != nil && *sl.Value == correct {
			winningSlot = sl.ID
		}
	}
	if err := f.svc.PlaceChip(f.ctx, f.tenant.ID, game.ID, b.ID, 1, &winningSlot, 0); err != nil {
		t.Fatal(err)
	}
	f.do(game.ID, ActionRequest{Action: ActionScore, FromPhase: PhaseBetting})

	snap, _ = f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	if snap.Scoring == nil {
		t.Fatal("the round scored but the snapshot carries no scoring block")
	}
	if got := snap.Standings[a.ID]; got != 500 {
		t.Fatalf("the team with the right answer has %d, want 500", got)
	}
	if got := snap.Standings[b.ID]; got != 200 {
		t.Fatalf("the team whose $200 chip won has %d, want 200", got)
	}
}

// The host clicked twice. The second click must be refused, not silently
// skip a question.
func TestDoubleClickedActionIsRefused(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())
	f.do(game.ID, ActionRequest{Action: ActionOpenLobby, FromPhase: PhaseSetup})
	f.join(game.ID, "Solo")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})

	snap, _ := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	cellID := snap.Board[0].ID
	f.do(game.ID, ActionRequest{Action: ActionPickCell, FromPhase: PhaseBoard, CellID: &cellID})

	_, err := f.svc.Do(f.ctx, f.tenant.ID, game.ID, ActionRequest{
		Action: ActionPickCell, FromPhase: PhaseBoard, CellID: &cellID,
	})
	if !errors.Is(err, ErrPhaseConflict) {
		t.Fatalf("second click returned %v, want ErrPhaseConflict", err)
	}
	rounds, err := ListRounds(f.ctx, f.pool, f.tenant.ID, game.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rounds) != 1 {
		t.Fatalf("%d rounds opened by a double click, want 1", len(rounds))
	}
}

// A stake larger than the team's bank is CLAMPED, not rejected: rejecting
// would let a table lose its final to a typo.
func TestFinalStakeIsClampedToTheBank(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	s := defaultSettings()
	s.BoardColumns, s.BoardRows = 1, 1
	s.CellValues = []int{500}
	game := f.newGame(s, []string{"space"})
	f.do(game.ID, ActionRequest{Action: ActionOpenLobby, FromPhase: PhaseSetup})
	a := f.join(game.ID, "Bar Flies")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})

	// Play the one board question so the team has a bank of exactly $500.
	snap, _ := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	f.playOneRound(game, map[uuid.UUID]string{a.ID: FormatValue(snapCorrect(t, f, game))})
	_ = snap
	f.do(game.ID, ActionRequest{Action: ActionNext, FromPhase: PhaseScoring})

	snap, _ = f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	bank := snap.Standings[a.ID]
	if bank <= 0 {
		t.Fatalf("team bank is %d — the setup did not earn anything to stake", bank)
	}

	f.do(game.ID, ActionRequest{Action: ActionFinal, FromPhase: PhaseBoard})
	huge := bank * 10
	if err := f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, a.ID, "1", &huge); err != nil {
		t.Fatalf("SubmitAnswer with an oversized stake: %v", err)
	}
	g := f.reload(game.ID)
	answers, err := ListAnswers(f.ctx, f.pool, f.tenant.ID, *g.CurrentRoundID)
	if err != nil {
		t.Fatal(err)
	}
	if len(answers) != 1 || answers[0].Stake == nil {
		t.Fatalf("answers = %+v", answers)
	}
	if *answers[0].Stake != bank {
		t.Fatalf("stake stored as %d, want it clamped to the bank %d", *answers[0].Stake, bank)
	}
}

// snapCorrect reads the in-play round's answer out of the host's view.
func snapCorrect(t *testing.T, f *fixture, game *Game) float64 {
	t.Helper()
	g := f.reload(game.ID)
	if g.CurrentRoundID == nil {
		// No round open yet: peek at the first unplayed cell's question.
		cells, err := ListBoardCells(f.ctx, f.pool, f.tenant.ID, game.ID)
		if err != nil || len(cells) == 0 {
			t.Fatalf("no board: %v", err)
		}
		q, err := GetQuestion(f.ctx, f.pool, f.tenant.ID, cells[0].QuestionID)
		if err != nil {
			t.Fatal(err)
		}
		return q.AnswerValue
	}
	round, err := GetRound(f.ctx, f.pool, f.tenant.ID, *g.CurrentRoundID)
	if err != nil {
		t.Fatal(err)
	}
	// The round's own copy — the same value the engine will mark against.
	return round.AnswerValue
}

// A stake cannot be changed once the answer phase has closed -- that is what
// separates a wager from a calculation.
func TestStakeCannotChangeAfterTheAnswerPhaseCloses(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	s := defaultSettings()
	s.BoardColumns, s.BoardRows = 1, 1
	s.CellValues = []int{500}
	game := f.newGame(s, []string{"space"})
	f.do(game.ID, ActionRequest{Action: ActionOpenLobby, FromPhase: PhaseSetup})
	a := f.join(game.ID, "Bar Flies")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})
	f.playOneRound(game, map[uuid.UUID]string{a.ID: FormatValue(snapCorrect(t, f, game))})
	f.do(game.ID, ActionRequest{Action: ActionNext, FromPhase: PhaseScoring})
	f.do(game.ID, ActionRequest{Action: ActionFinal, FromPhase: PhaseBoard})

	stake := 100
	if err := f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, a.ID, "1", &stake); err != nil {
		t.Fatal(err)
	}
	// The one team answered, so the phase closed early.
	if g := f.reload(game.ID); g.Phase == PhaseQuestion {
		f.do(game.ID, ActionRequest{Action: ActionReveal, FromPhase: PhaseQuestion})
	}
	bigger := 400
	err := f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, a.ID, "1", &bigger)
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("re-staking after the answer phase returned %v, want ErrClosed", err)
	}
}

// A team that joined during the final cannot stake into it: it is not in that
// round's denominator, and letting it wager would put money on a question it
// was excluded from.
func TestTeamJoiningDuringTheFinalCannotStake(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	s := defaultSettings()
	s.BoardColumns, s.BoardRows = 1, 1
	s.CellValues = []int{500}
	game := f.newGame(s, []string{"space"})
	f.do(game.ID, ActionRequest{Action: ActionOpenLobby, FromPhase: PhaseSetup})
	a := f.join(game.ID, "Bar Flies")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})
	f.playOneRound(game, map[uuid.UUID]string{a.ID: FormatValue(snapCorrect(t, f, game))})
	f.do(game.ID, ActionRequest{Action: ActionNext, FromPhase: PhaseScoring})
	f.do(game.ID, ActionRequest{Action: ActionFinal, FromPhase: PhaseBoard})

	late := f.join(game.ID, "Latecomers")
	stake := 100
	err := f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, late.ID, "1", &stake)
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("a team that joined mid-final was allowed to stake: %v", err)
	}
}

// With final_wager off, an emptied board goes STRAIGHT to the podium and the
// "final" action is refused. This is the path a first-ever night runs.
func TestFinalWagerOffGoesStraightToPodium(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	s := defaultSettings()
	s.BoardColumns, s.BoardRows = 1, 1
	s.CellValues = []int{500}
	s.FinalWager = false
	game := f.newGame(s, []string{"space"})
	f.do(game.ID, ActionRequest{Action: ActionOpenLobby, FromPhase: PhaseSetup})
	a := f.join(game.ID, "Bar Flies")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})
	f.playOneRound(game, map[uuid.UUID]string{a.ID: "1"})

	f.do(game.ID, ActionRequest{Action: ActionNext, FromPhase: PhaseScoring})
	if g := f.reload(game.ID); g.Phase != PhasePodium {
		t.Fatalf("phase = %s with the board empty and no final, want podium", g.Phase)
	}
	_, err := f.svc.Do(f.ctx, f.tenant.ID, game.ID, ActionRequest{Action: ActionFinal, FromPhase: PhasePodium})
	if err == nil {
		t.Fatal("the final action was accepted on a game with final_wager off")
	}
}

// The proof that the deadline is server-authoritative: nothing but the row
// decides when a phase ends, so a sweep with no host present advances it.
func TestExpiredDeadlineAdvancesWithNobodyClicking(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())
	f.do(game.ID, ActionRequest{Action: ActionOpenLobby, FromPhase: PhaseSetup})
	f.join(game.ID, "Bar Flies")
	f.join(game.ID, "Quiz Khalifa")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})

	snap, _ := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	cellID := snap.Board[0].ID
	f.do(game.ID, ActionRequest{Action: ActionPickCell, FromPhase: PhaseBoard, CellID: &cellID})

	// Drag the deadline into the past, as if the clock had run out with the
	// host's laptop asleep.
	past := time.Now().UTC().Add(-10 * time.Second)
	if _, err := f.pool.Exec(f.ctx,
		`UPDATE app_trivia_games SET phase_deadline = $3 WHERE tenant_id = $1 AND id = $2`,
		f.tenant.ID, game.ID, past); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SweepDue(f.ctx, f.tenant.ID, game.ID); err != nil {
		t.Fatalf("SweepDue: %v", err)
	}
	if g := f.reload(game.ID); g.Phase != PhaseReveal {
		t.Fatalf("phase = %s after the answer clock expired, want reveal", g.Phase)
	}
	// And the cards were built exactly once, by the sweep that won.
	g := f.reload(game.ID)
	slots, err := ListSlots(f.ctx, f.pool, f.tenant.ID, *g.CurrentRoundID)
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 1 {
		t.Fatalf("got %d cards for a round nobody answered, want just the pseudo-slot", len(slots))
	}
}

// The grace window: a deadline that has only just passed is not yet closed,
// so a phone submitting at T-0.2s over bar wifi still lands.
func TestDeadlineHasAGraceWindow(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())
	f.do(game.ID, ActionRequest{Action: ActionOpenLobby, FromPhase: PhaseSetup})
	a := f.join(game.ID, "Bar Flies")
	f.join(game.ID, "Quiz Khalifa")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})
	snap, _ := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	cellID := snap.Board[0].ID
	f.do(game.ID, ActionRequest{Action: ActionPickCell, FromPhase: PhaseBoard, CellID: &cellID})

	justPast := time.Now().UTC().Add(-200 * time.Millisecond)
	if _, err := f.pool.Exec(f.ctx,
		`UPDATE app_trivia_games SET phase_deadline = $3 WHERE tenant_id = $1 AND id = $2`,
		f.tenant.ID, game.ID, justPast); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, a.ID, "42", nil); err != nil {
		t.Fatalf("a submission 200ms past the deadline was refused: %v", err)
	}
}

// Two chips on one answer is refused, and it is the unique index that
// refuses it -- so two racing taps cannot both succeed.
func TestBothChipsOnOneAnswerIsRefused(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())
	f.do(game.ID, ActionRequest{Action: ActionOpenLobby, FromPhase: PhaseSetup})
	a := f.join(game.ID, "Bar Flies")
	b := f.join(game.ID, "Quiz Khalifa")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})
	snap, _ := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	cellID := snap.Board[0].ID
	f.do(game.ID, ActionRequest{Action: ActionPickCell, FromPhase: PhaseBoard, CellID: &cellID})
	if err := f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, a.ID, "10", nil); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, b.ID, "20", nil); err != nil {
		t.Fatal(err)
	}
	f.do(game.ID, ActionRequest{Action: ActionOpenBetting, FromPhase: PhaseReveal})

	snap, _ = f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	target := snap.Slots[1].ID
	if err := f.svc.PlaceChip(f.ctx, f.tenant.ID, game.ID, a.ID, 0, &target, 0); err != nil {
		t.Fatalf("first chip: %v", err)
	}
	err := f.svc.PlaceChip(f.ctx, f.tenant.ID, game.ID, a.ID, 1, &target, 0)
	if err == nil {
		t.Fatal("both chips landed on one answer")
	}
	if !strings.Contains(err.Error(), "different answers") {
		t.Fatalf("error = %v, want it to explain the spread rule", err)
	}
}

// Moving a chip is an UPDATE, so a double-tap cannot double a team's money.
func TestMovingAChipDoesNotDuplicateIt(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())
	f.do(game.ID, ActionRequest{Action: ActionOpenLobby, FromPhase: PhaseSetup})
	a := f.join(game.ID, "Bar Flies")
	b := f.join(game.ID, "Quiz Khalifa")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})
	snap, _ := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	cellID := snap.Board[0].ID
	f.do(game.ID, ActionRequest{Action: ActionPickCell, FromPhase: PhaseBoard, CellID: &cellID})
	_ = f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, a.ID, "10", nil)
	_ = f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, b.ID, "20", nil)
	f.do(game.ID, ActionRequest{Action: ActionOpenBetting, FromPhase: PhaseReveal})

	snap, _ = f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	first, second := snap.Slots[1].ID, snap.Slots[2].ID
	for range 5 {
		if err := f.svc.PlaceChip(f.ctx, f.tenant.ID, game.ID, a.ID, 0, &first, 0); err != nil {
			t.Fatal(err)
		}
		if err := f.svc.PlaceChip(f.ctx, f.tenant.ID, game.ID, a.ID, 0, &second, 0); err != nil {
			t.Fatal(err)
		}
	}
	g := f.reload(game.ID)
	bets, err := ListBets(f.ctx, f.pool, f.tenant.ID, *g.CurrentRoundID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bets) != 1 {
		t.Fatalf("%d chips on the board after ten taps of one chip, want 1", len(bets))
	}
}

// The twenty-first team is refused.
func TestGameIsCappedAtTwentyTeams(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), nil)
	f.do(game.ID, ActionRequest{Action: ActionOpenLobby, FromPhase: PhaseSetup})
	for i := range MaxTeams {
		f.join(game.ID, "Team "+FormatValue(float64(i)))
	}
	_, _, err := f.svc.Join(f.ctx, f.tenant.ID, game.ID, "One Too Many")
	if !errors.Is(err, ErrGameFull) {
		t.Fatalf("the 21st team got %v, want ErrGameFull", err)
	}
}

// Two tables typing the same name: the index decides, not a read-then-write.
func TestDuplicateTeamNameIsRefused(t *testing.T) {
	f := newFixture(t)
	game := f.newGame(defaultSettings(), nil)
	f.do(game.ID, ActionRequest{Action: ActionOpenLobby, FromPhase: PhaseSetup})
	f.join(game.ID, "Bar Flies")
	_, _, err := f.svc.Join(f.ctx, f.tenant.ID, game.ID, "  bar   flies  ")
	if !errors.Is(err, ErrNameTaken) {
		t.Fatalf("got %v, want ErrNameTaken — names differing only in case and space are one name", err)
	}
}

// A team joining mid-question is out of THAT question's denominator, so the
// counter never ticks backwards.
func TestLateJoinerIsNotInTheCurrentDenominator(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())
	f.do(game.ID, ActionRequest{Action: ActionOpenLobby, FromPhase: PhaseSetup})
	f.join(game.ID, "Bar Flies")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})
	snap, _ := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	cellID := snap.Board[0].ID
	f.do(game.ID, ActionRequest{Action: ActionPickCell, FromPhase: PhaseBoard, CellID: &cellID})

	before, _ := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	f.join(game.ID, "Latecomers")
	after, _ := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)

	if after.Round.EligibleCount != before.Round.EligibleCount {
		t.Fatalf("denominator moved from %d to %d when a team joined mid-question",
			before.Round.EligibleCount, after.Round.EligibleCount)
	}
	if len(after.Teams) != 2 {
		t.Fatalf("the latecomer is not on the TV at all (%d teams)", len(after.Teams))
	}
}

// A game name in one workspace is invisible from another.
func TestGameNamesAreTenantScoped(t *testing.T) {
	f := newFixture(t)
	other := newFixture(t)
	game := f.newGame(defaultSettings(), nil)

	if _, err := GetGameByName(f.ctx, f.pool, f.tenant.ID, game.Name); err != nil {
		t.Fatalf("own tenant cannot see its own game: %v", err)
	}
	_, err := GetGameByName(other.ctx, other.pool, other.tenant.ID, game.Name)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("another tenant resolved the game: %v", err)
	}
}
