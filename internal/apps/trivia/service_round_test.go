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
		if err := f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, teamID, raw); err != nil {
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
	if err := f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, a.ID, FormatValue(correct)); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, b.ID, FormatValue(correct+50)); err != nil {
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

	// B backs A's correct card with the $200 chip and leaves the $100 in
	// hand, so the assertion below is about that one chip and nothing else.
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

// openFinal drives a one-cell game through its board round and into the
// final's WAGER phase, which is where every final test now starts: the room
// has money, the category is up, and the question is not.
func (f *fixture) openFinal(game *Game, teams ...*Team) *Game {
	f.t.Helper()
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})
	answers := map[uuid.UUID]string{}
	correct := snapCorrect(f.t, f, game)
	for i, tm := range teams {
		// The first team writes the winning answer and takes the cell, so the
		// room goes into the final with an uneven set of banks.
		answers[tm.ID] = FormatValue(correct - float64(i))
	}
	f.playOneRound(game, answers)
	f.do(game.ID, ActionRequest{Action: ActionNext, FromPhase: PhaseScoring})
	// The boards are done, so the night is at the break that precedes the
	// final -- the biggest one of the night. The final opens out of it.
	f.do(game.ID, ActionRequest{Action: ActionFinal, FromPhase: PhaseIntermission})
	g := f.reload(game.ID)
	if g.Phase != PhaseWager {
		f.t.Fatalf("the final opened into %s, want wager — the bet must precede the question", g.Phase)
	}
	return g
}

// oneCellSettings is the smallest game that still reaches a final: one board
// question to earn a bank with, then the final.
func oneCellSettings() Settings {
	s := defaultSettings()
	s.BoardColumns, s.BoardRows = 1, 1
	s.CellValues = []int{500}
	return s
}

// The whole shape of the final, end to end: the wager lands before the
// question exists, the question then opens, the answer carries no stake, and
// the chip in betting is worth exactly what was locked a phase earlier.
func TestFinalWagerPrecedesTheQuestion(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(oneCellSettings(), []string{"space"})
	a := f.join(game.ID, "Bar Flies")
	b := f.join(game.ID, "Quiz Khalifa")
	f.openFinal(game, a, b)

	// Nothing about the question is public while the wager is open.
	snap, _ := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	if snap.Round == nil || snap.Round.Topic == "" {
		t.Fatalf("the wager phase has no category to bet against: %+v", snap.Round)
	}
	banks := map[uuid.UUID]int{a.ID: snap.Standings[a.ID], b.ID: snap.Standings[b.ID]}
	if banks[a.ID] <= 0 {
		t.Fatalf("the winner's bank is %d — the setup earned nothing to wager", banks[a.ID])
	}

	// A wants half of it; B tries for ten times its bank and is clamped.
	half := banks[a.ID] / 2
	if err := f.svc.SetWager(f.ctx, f.tenant.ID, game.ID, a.ID, half); err != nil {
		t.Fatalf("SetWager: %v", err)
	}
	if err := f.svc.SetWager(f.ctx, f.tenant.ID, game.ID, b.ID, banks[b.ID]*10+1000); err != nil {
		t.Fatalf("SetWager (oversized): %v", err)
	}

	// Both eligible tables are in, so the wager phase closed itself and the
	// question is now on the wall.
	g := f.reload(game.ID)
	if g.Phase != PhaseQuestion {
		t.Fatalf("phase = %s after every table locked in, want question", g.Phase)
	}
	wagers, err := ListWagers(f.ctx, f.pool, f.tenant.ID, *g.CurrentRoundID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[uuid.UUID]int{}
	for _, w := range wagers {
		got[w.TeamID] = w.Amount
	}
	if got[a.ID] != half {
		t.Fatalf("wager stored as %d, want %d", got[a.ID], half)
	}
	if got[b.ID] != banks[b.ID] {
		t.Fatalf("oversized wager stored as %d, want it clamped to the bank %d", got[b.ID], banks[b.ID])
	}

	// The answers carry no stake at all now.
	correct := snapCorrect(t, f, game)
	if err := f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, a.ID, FormatValue(correct)); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, b.ID, FormatValue(correct+1000)); err != nil {
		t.Fatal(err)
	}
	if g := f.reload(game.ID); g.Phase == PhaseQuestion {
		f.do(game.ID, ActionRequest{Action: ActionReveal, FromPhase: PhaseQuestion})
	}
	f.do(game.ID, ActionRequest{Action: ActionOpenBetting, FromPhase: PhaseReveal})

	// One chip each, and the server prices it off the wager row.
	snap, _ = f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	target := snap.Slots[0].ID
	for _, tm := range []*Team{a, b} {
		if err := f.svc.PlaceChip(f.ctx, f.tenant.ID, game.ID, tm.ID, 0, &target, 0); err != nil {
			t.Fatalf("PlaceChip: %v", err)
		}
	}
	snap, _ = f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	placed := map[uuid.UUID]int{}
	for _, bet := range snap.Bets {
		placed[bet.TeamID] = bet.Amount
	}
	if placed[a.ID] != half || placed[b.ID] != banks[b.ID] {
		t.Fatalf("chips are worth %v, want the locked wagers %d and %d",
			placed, half, banks[b.ID])
	}
}

// A wager cannot be placed once the question is on the wall. THIS is the
// mechanic: a bet a table can revise after reading the question is not a bet.
func TestWagerIsRefusedOnceTheQuestionIsUp(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(oneCellSettings(), []string{"space"})
	a := f.join(game.ID, "Bar Flies")
	f.join(game.ID, "Quiz Khalifa")
	f.openFinal(game, a)

	if err := f.svc.SetWager(f.ctx, f.tenant.ID, game.ID, a.ID, 100); err != nil {
		t.Fatal(err)
	}
	// Two tables are eligible and only one has locked, so the phase is still
	// open; the host asks the question anyway.
	f.do(game.ID, ActionRequest{Action: ActionAsk, FromPhase: PhaseWager})
	if g := f.reload(game.ID); g.Phase != PhaseQuestion {
		t.Fatalf("phase = %s after the host asked the question, want question", g.Phase)
	}
	if err := f.svc.SetWager(f.ctx, f.tenant.ID, game.ID, a.ID, 400); !errors.Is(err, ErrClosed) {
		t.Fatalf("re-wagering after the question went up returned %v, want ErrClosed", err)
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

// Everybody locked in, so the clock is cut short. A room that has already
// decided should not sit out twenty more seconds of silence before the one
// moment of the night everybody came for.
func TestWagerPhaseClosesEarlyWhenEveryTableHasLocked(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(oneCellSettings(), []string{"space"})
	a := f.join(game.ID, "Bar Flies")
	b := f.join(game.ID, "Quiz Khalifa")
	f.openFinal(game, a, b)

	if err := f.svc.SetWager(f.ctx, f.tenant.ID, game.ID, a.ID, 100); err != nil {
		t.Fatal(err)
	}
	if g := f.reload(game.ID); g.Phase != PhaseWager {
		t.Fatalf("phase = %s with one of two tables locked in, want wager", g.Phase)
	}
	if err := f.svc.SetWager(f.ctx, f.tenant.ID, game.ID, b.ID, 0); err != nil {
		t.Fatal(err)
	}
	g := f.reload(game.ID)
	if g.Phase != PhaseQuestion {
		t.Fatalf("phase = %s once both tables were in, want question", g.Phase)
	}
	// $0 is a real wager, not an absence: it still counts as being in.
	if g.PhaseDeadline == nil {
		t.Fatal("the question opened with no clock")
	}
}

// Once the final is under way the door is shut. A table arriving then cannot
// answer the final, cannot wager into it, and the next thing that happens is
// the podium -- so the honest answer at the door is no, not a phone that says
// "waiting" until the lights come up.
//
// The refusal has to hold through EVERY phase the final passes through, not
// just the question: it opens on the wager, re-enters the ordinary question
// phase and travels out through reveal, betting and scoring, and the rule
// keys off the round being final rather than the phase name so all of them
// are covered at once.
func TestJoinIsRefusedOnceTheFinalBegins(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(oneCellSettings(), []string{"space"})
	a := f.join(game.ID, "Bar Flies")
	f.openFinal(game, a)

	phases := []struct {
		label string
		enter func()
	}{
		{"wager", func() {}},
		{"question", func() { f.do(game.ID, ActionRequest{Action: ActionAsk, FromPhase: PhaseWager}) }},
		{"reveal", func() { f.do(game.ID, ActionRequest{Action: ActionReveal, FromPhase: PhaseQuestion}) }},
		{"betting", func() { f.do(game.ID, ActionRequest{Action: ActionOpenBetting, FromPhase: PhaseReveal}) }},
		{"scoring", func() { f.do(game.ID, ActionRequest{Action: ActionScore, FromPhase: PhaseBetting}) }},
	}
	for _, p := range phases {
		p.enter()
		_, _, err := f.svc.Join(f.ctx, f.tenant.ID, game.ID, "Latecomers "+p.label)
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("%s: a table joined while the final was under way: %v", p.label, err)
		}
		if !strings.Contains(err.Error(), "this game is closing") {
			t.Fatalf("%s: refusal reads %q, which does not tell the phone why", p.label, err)
		}
	}
}

// A team that is not in the final's denominator cannot wager into it: letting
// it bet would put money on a question it was excluded from. It must also not
// hold the wager phase open. Belt and braces behind the closed door above:
// Join refuses such a table now, so the row is made ineligible by hand -- the
// gate on the wager path must hold however the row got there.
func TestTeamJoiningDuringTheFinalCannotWager(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(oneCellSettings(), []string{"space"})
	a := f.join(game.ID, "Bar Flies")
	late := f.join(game.ID, "Latecomers")
	if _, err := f.pool.Exec(f.ctx,
		`UPDATE app_trivia_teams SET eligible_from_ordinal = 99 WHERE tenant_id = $1 AND id = $2`,
		f.tenant.ID, late.ID); err != nil {
		t.Fatal(err)
	}
	f.openFinal(game, a)

	if err := f.svc.SetWager(f.ctx, f.tenant.ID, game.ID, late.ID, 100); !errors.Is(err, ErrClosed) {
		t.Fatalf("a team that joined mid-final was allowed to wager: %v", err)
	}
	// And the one eligible table locking in still closes the phase, rather
	// than the room waiting on a table that cannot play.
	if err := f.svc.SetWager(f.ctx, f.tenant.ID, game.ID, a.ID, 100); err != nil {
		t.Fatal(err)
	}
	if g := f.reload(game.ID); g.Phase != PhaseQuestion {
		t.Fatalf("phase = %s — a latecomer held the wager phase open", g.Phase)
	}
}

// The chip had NO eligibility check at all: a table that joined mid-question
// was correctly barred from answering and then walked straight into the
// betting on that same question -- which in this game is the half of the round
// that actually pays.
func TestLateJoinerCannotBetOnTheQuestionItMissed(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())
	early := f.join(game.ID, "Bar Flies")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})
	snap, _ := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	cellID := snap.Board[0].ID
	f.do(game.ID, ActionRequest{Action: ActionPickCell, FromPhase: PhaseBoard, CellID: &cellID})

	late := f.join(game.ID, "Latecomers")
	if err := f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, early.ID, "10"); err != nil {
		t.Fatal(err)
	}
	// The only eligible table has answered, so the question may already have
	// closed itself — which is the point: the latecomer is not counted.
	if f.reload(game.ID).Phase == PhaseQuestion {
		f.do(game.ID, ActionRequest{Action: ActionReveal, FromPhase: PhaseQuestion})
	}
	f.do(game.ID, ActionRequest{Action: ActionOpenBetting, FromPhase: PhaseReveal})

	snap, _ = f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	slot := snap.Slots[0].ID
	err := f.svc.PlaceChip(f.ctx, f.tenant.ID, game.ID, late.ID, 0, &slot, 0)
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("a table that joined mid-question placed a chip on it: %v", err)
	}
	// The table that was here all along is of course unaffected.
	if err := f.svc.PlaceChip(f.ctx, f.tenant.ID, game.ID, early.ID, 0, &slot, 0); err != nil {
		t.Fatalf("an eligible table could not place a chip: %v", err)
	}
}

// With final_wager off, an emptied board WAITS rather than ending itself, and
// the "final" action stays refused. This is the path a first-ever night runs.
//
// It used to jump to the podium. That took the decision off the host at the
// one moment they most want it -- the room is up for more, the board is spent
// -- and the podium is still one click away.
func TestFinalWagerOffWaitsOnTheEmptiedBoard(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	s := defaultSettings()
	s.BoardColumns, s.BoardRows = 1, 1
	s.CellValues = []int{500}
	s.FinalWager = false
	game := f.newGame(s, []string{"space"})
	a := f.join(game.ID, "Bar Flies")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})
	f.playOneRound(game, map[uuid.UUID]string{a.ID: "1"})

	f.do(game.ID, ActionRequest{Action: ActionNext, FromPhase: PhaseScoring})
	if g := f.reload(game.ID); g.Phase != PhaseBoard {
		t.Fatalf("phase = %s with the board empty and no final, want it waiting on the board", g.Phase)
	}
	_, err := f.svc.Do(f.ctx, f.tenant.ID, game.ID, ActionRequest{Action: ActionFinal, FromPhase: PhaseBoard})
	if err == nil {
		t.Fatal("the final action was accepted on a game with final_wager off")
	}
	// And the host ends it when they mean to, from the button the console has
	// been offering for this state all along.
	f.do(game.ID, ActionRequest{Action: ActionFinish, FromPhase: PhaseBoard})
	if g := f.reload(game.ID); g.Phase != PhasePodium {
		t.Fatalf("phase = %s after the host called it, want podium", g.Phase)
	}
}

// The proof that the deadline is server-authoritative: nothing but the row
// decides when a phase ends, so a sweep with no host present advances it.
func TestExpiredDeadlineAdvancesWithNobodyClicking(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())
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
	if err := f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, a.ID, "42"); err != nil {
		t.Fatalf("a submission 200ms past the deadline was refused: %v", err)
	}
}

// Both chips on one answer is a legal bet: migration 097 dropped the index
// that used to forbid it, so a sure table can go all-in on one card.
func TestBothChipsCanStackOnOneAnswer(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())
	a := f.join(game.ID, "Bar Flies")
	b := f.join(game.ID, "Quiz Khalifa")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})
	snap, _ := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	cellID := snap.Board[0].ID
	f.do(game.ID, ActionRequest{Action: ActionPickCell, FromPhase: PhaseBoard, CellID: &cellID})
	if err := f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, a.ID, "10"); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, b.ID, "20"); err != nil {
		t.Fatal(err)
	}
	f.do(game.ID, ActionRequest{Action: ActionOpenBetting, FromPhase: PhaseReveal})

	snap, _ = f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	target := snap.Slots[1].ID
	if err := f.svc.PlaceChip(f.ctx, f.tenant.ID, game.ID, a.ID, 0, &target, 0); err != nil {
		t.Fatalf("first chip: %v", err)
	}
	if err := f.svc.PlaceChip(f.ctx, f.tenant.ID, game.ID, a.ID, 1, &target, 0); err != nil {
		t.Fatalf("second chip on the same answer: %v", err)
	}

	bets, err := ListBets(f.ctx, f.pool, f.tenant.ID, *f.reload(game.ID).CurrentRoundID)
	if err != nil {
		t.Fatal(err)
	}
	stacked := 0
	for _, bet := range bets {
		if bet.TeamID == a.ID && bet.SlotID == target {
			stacked += bet.Amount
		}
	}
	if stacked != 300 {
		t.Fatalf("stacked %d on one card, want both chips (300)", stacked)
	}
}

// Moving a chip is an UPDATE, so a double-tap cannot double a team's money.
func TestMovingAChipDoesNotDuplicateIt(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())
	a := f.join(game.ID, "Bar Flies")
	b := f.join(game.ID, "Quiz Khalifa")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})
	snap, _ := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	cellID := snap.Board[0].ID
	f.do(game.ID, ActionRequest{Action: ActionPickCell, FromPhase: PhaseBoard, CellID: &cellID})
	_ = f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, a.ID, "10")
	_ = f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, b.ID, "20")
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

// Betting closes as soon as every eligible table has placed, without the host
// touching anything. The chips are on the wall as they land now, so a broken
// early close is no longer a room staring at empty cards — but it is still
// everyone waiting out a clock with nothing left to decide.
func TestBettingClosesEarlyWhenEveryTableHasPlaced(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())
	a := f.join(game.ID, "Bar Flies")
	b := f.join(game.ID, "Quiz Khalifa")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})

	snap, _ := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	cellID := snap.Board[0].ID
	f.do(game.ID, ActionRequest{Action: ActionPickCell, FromPhase: PhaseBoard, CellID: &cellID})
	if err := f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, a.ID, "10"); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, b.ID, "20"); err != nil {
		t.Fatal(err)
	}
	f.do(game.ID, ActionRequest{Action: ActionOpenBetting, FromPhase: PhaseReveal})

	snap, _ = f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	slots := snap.Slots
	if len(slots) < 3 {
		t.Fatalf("got %d cards, want at least 3", len(slots))
	}
	place := func(team uuid.UUID, chip int, slot uuid.UUID) {
		t.Helper()
		if err := f.svc.PlaceChip(f.ctx, f.tenant.ID, game.ID, team, chip, &slot, 0); err != nil {
			t.Fatalf("placing chip: %v", err)
		}
	}
	place(a.ID, 0, slots[1].ID)
	place(a.ID, 1, slots[2].ID)
	if g := f.reload(game.ID); g.Phase != PhaseBetting {
		t.Fatalf("phase = %s with one table still to bet, want betting", g.Phase)
	}
	place(b.ID, 0, slots[0].ID)
	place(b.ID, 1, slots[1].ID)

	if g := f.reload(game.ID); g.Phase != PhaseScoring {
		t.Fatalf("phase = %s once every table had placed, want scoring", g.Phase)
	}
	// And the chips are now visible, which is the beat the hiding was for.
	scored, _ := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	shown := 0
	for _, sl := range ProjectDisplay(scored).Slots {
		shown += len(sl.Chips)
	}
	if shown != 4 {
		t.Fatalf("the TV shows %d chips after betting closed, want all 4", shown)
	}
}

// A game cannot start without a board. Starting into an empty board puts the
// room in front of a screen with nothing to pick and the only way out is to
// end the game.
func TestCannotStartWithoutABoard(t *testing.T) {
	f := newFixture(t)
	game := f.newGame(defaultSettings(), nil) // no board built

	_, err := f.svc.Do(f.ctx, f.tenant.ID, game.ID,
		ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})
	if !errors.Is(err, ErrBadRequest) {
		t.Fatalf("starting with no board returned %v, want ErrBadRequest", err)
	}
	if !strings.Contains(err.Error(), "no board") {
		t.Fatalf("error %q does not say what is wrong", err)
	}
	if g := f.reload(game.ID); g.Phase != PhaseLobby {
		t.Fatalf("phase = %s after a refused start, want lobby", g.Phase)
	}

	// With a board it starts.
	f.seedBank(topicSet(), 4)
	f.buildBoard(game, topicSet())
	if _, err := f.svc.Do(f.ctx, f.tenant.ID, game.ID,
		ActionRequest{Action: ActionStart, FromPhase: PhaseLobby}); err != nil {
		t.Fatalf("starting with a board: %v", err)
	}
}
