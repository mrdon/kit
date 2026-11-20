package trivia

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// Practice plays a whole round from the lobby -- money and all -- and then
// leaves nothing behind: back in the lobby, every score zero, no round rows,
// and the first real question is question one.
func TestPracticeRoundPlaysThroughAndLeavesNoTrace(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())
	a := f.join(game.ID, "Bar Flies")
	b := f.join(game.ID, "Quiz Khalifa")

	snap := f.do(game.ID, ActionRequest{Action: ActionPractice, FromPhase: PhaseLobby})
	if snap.Phase != PhaseQuestion || !snap.Practice || snap.Round == nil {
		t.Fatalf("practice opened into phase=%s practice=%v round=%v", snap.Phase, snap.Practice, snap.Round)
	}
	for _, c := range snap.Board {
		if c.Played {
			t.Fatal("practice played a board cell")
		}
	}
	correct := snap.Round.CorrectValue

	// Both tables answer, and practice must NOT close early on it: the host
	// is still talking.
	for _, team := range []*Team{a, b} {
		if err := f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, team.ID, FormatValue(correct)); err != nil {
			t.Fatal(err)
		}
	}
	if g := f.reload(game.ID); g.Phase != PhaseQuestion {
		t.Fatalf("practice closed itself to %s when every table was in", g.Phase)
	}
	// A table walking in mid-practice plays the real game from question one.
	late := f.join(game.ID, "Latecomers")

	f.do(game.ID, ActionRequest{Action: ActionReveal, FromPhase: PhaseQuestion})
	f.do(game.ID, ActionRequest{Action: ActionOpenBetting, FromPhase: PhaseReveal})
	snap = f.do(game.ID, ActionRequest{Action: ActionScore, FromPhase: PhaseBetting})
	if snap.Standings[a.ID] != 500 {
		t.Fatalf("practice paid the right answer %d, want the first row's 500", snap.Standings[a.ID])
	}

	snap = f.do(game.ID, ActionRequest{Action: ActionNext, FromPhase: PhaseScoring})
	if snap.Phase != PhaseLobby || snap.Practice || snap.Round != nil {
		t.Fatalf("after practice: phase=%s practice=%v round=%v", snap.Phase, snap.Practice, snap.Round)
	}
	for id, total := range snap.Standings {
		if total != 0 {
			t.Fatalf("team %s kept %d from practice", id, total)
		}
	}
	rounds, err := ListRounds(f.ctx, f.pool, f.tenant.ID, game.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rounds) != 0 {
		t.Fatalf("%d round rows survived practice", len(rounds))
	}

	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})
	g := f.playOneRound(f.reload(game.ID), nil)
	round, err := GetRound(f.ctx, f.pool, f.tenant.ID, *g.CurrentRoundID)
	if err != nil {
		t.Fatal(err)
	}
	if round.Ordinal != 1 {
		t.Fatalf("first real question is ordinal %d, want 1", round.Ordinal)
	}
	if err := f.svc.assertEligible(f.ctx, f.tenant.ID, game.ID, late.ID, round); err != nil {
		t.Fatalf("a table that joined during practice is shut out of question one: %v", err)
	}
}

// Practice is a lobby thing. Once the board is up it is refused.
func TestPracticeIsRefusedOnceTheGameStarts(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())
	f.join(game.ID, "Bar Flies")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})
	if _, err := f.svc.Do(f.ctx, f.tenant.ID, game.ID,
		ActionRequest{Action: ActionPractice, FromPhase: PhaseBoard}); err == nil {
		t.Fatal("practice was allowed from the board")
	}
}

// Pause takes the deadline away (so nothing can close the phase), extending
// while paused adds to the stopped clock, and resuming re-arms it.
func TestPauseStopsTheClockAndResumeRestartsIt(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())
	f.join(game.ID, "Bar Flies")
	f.do(game.ID, ActionRequest{Action: ActionPractice, FromPhase: PhaseLobby})

	snap := f.do(game.ID, ActionRequest{Action: ActionPause, FromPhase: PhaseQuestion})
	if snap.Deadline != nil || snap.PausedMs <= 0 {
		t.Fatalf("paused: deadline=%v pausedMs=%d", snap.Deadline, snap.PausedMs)
	}
	before := snap.PausedMs
	snap = f.do(game.ID, ActionRequest{Action: ActionExtend, FromPhase: PhaseQuestion, Seconds: 15})
	if snap.PausedMs != before+15000 {
		t.Fatalf("+15s while paused: pausedMs=%d, want %d", snap.PausedMs, before+15000)
	}
	// Nothing sweeps a paused phase.
	if err := f.svc.SweepDue(f.ctx, f.tenant.ID, game.ID); err != nil {
		t.Fatal(err)
	}
	if g := f.reload(game.ID); g.Phase != PhaseQuestion {
		t.Fatalf("a paused phase moved to %s", g.Phase)
	}

	snap = f.do(game.ID, ActionRequest{Action: ActionUnpause, FromPhase: PhaseQuestion})
	if snap.Deadline == nil || snap.PausedMs != 0 {
		t.Fatalf("resumed: deadline=%v pausedMs=%d", snap.Deadline, snap.PausedMs)
	}
	left := time.Until(*snap.Deadline)
	want := time.Duration(before+15000) * time.Millisecond
	if left > want || left < want-5*time.Second {
		t.Fatalf("resumed with %s left, want about %s", left, want)
	}

	// A pause does not survive a phase change.
	f.do(game.ID, ActionRequest{Action: ActionPause, FromPhase: PhaseQuestion})
	f.do(game.ID, ActionRequest{Action: ActionReveal, FromPhase: PhaseQuestion})
	if g := f.reload(game.ID); g.PausedRemainingMs != nil || g.PhaseDeadline == nil {
		t.Fatalf("the pause leaked into %s: paused=%v deadline=%v", g.Phase, g.PausedRemainingMs, g.PhaseDeadline)
	}
}

// Practice spends nothing: it reaches for a question an earlier night already
// asked, even over an easier one the room has not heard.
func TestPracticePrefersAQuestionAlreadyAsked(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	first := f.newGame(defaultSettings(), topicSet())
	team := f.join(first.ID, "Bar Flies")
	f.do(first.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})
	g := f.playOneRound(f.reload(first.ID), map[uuid.UUID]string{team.ID: "1"})
	asked, err := GetRound(f.ctx, f.pool, f.tenant.ID, *g.CurrentRoundID)
	if err != nil {
		t.Fatal(err)
	}

	// The asked question is the HARDEST in the bank; everything fresh is a 1.
	if _, err := f.pool.Exec(f.ctx, `
		UPDATE app_trivia_questions
		   SET difficulty = CASE WHEN prompt_key = $2 THEN 10 ELSE 1 END
		 WHERE tenant_id = $1`, f.tenant.ID, FoldKey(asked.Prompt)); err != nil {
		t.Fatal(err)
	}

	s := defaultSettings()
	s.RepeatQuestions = false // so the second board cannot hold the asked question
	second := f.newGame(s, topicSet())
	snap := f.do(second.ID, ActionRequest{Action: ActionPractice, FromPhase: PhaseLobby})
	if snap.Round == nil || snap.Round.Text != asked.Prompt {
		t.Fatalf("practice asked %v, want the question the first night already used (%q)", snap.Round, asked.Prompt)
	}
}
