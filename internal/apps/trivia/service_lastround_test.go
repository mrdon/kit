package trivia

import (
	"testing"

	"github.com/google/uuid"
)

// The bug this covers, end to end against a real database: the host scores a
// round, presses next, and the game sits on the board with current_round_id
// cleared. The console then has no round and no scoring block -- and that is
// the exact moment the host has to say which table picks the next category.
func TestSnapshotRemembersTheLastScoredRoundOnTheBoard(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())
	a := f.join(game.ID, "Bar Flies")
	b := f.join(game.ID, "Quiz Khalifa")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})

	// Nothing scored yet: no memory to have.
	snap, err := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snap.LastRound != nil {
		t.Fatalf("no round has been played but the snapshot carries %+v", snap.LastRound)
	}
	if ProjectHost(snap).LastRound != nil {
		t.Fatal("the host frame invents a last round before the first question")
	}

	correct := snapCorrect(t, f, game)
	f.playOneRound(game, map[uuid.UUID]string{
		a.ID: FormatValue(correct),
		b.ID: FormatValue(correct + 50),
	})

	// Press next. The board is where the pick happens.
	f.do(game.ID, ActionRequest{Action: ActionNext, FromPhase: PhaseScoring})
	g := f.reload(game.ID)
	if g.Phase != PhaseBoard {
		t.Fatalf("phase = %s after next, want board", g.Phase)
	}
	if g.CurrentRoundID != nil {
		t.Fatal("next no longer clears the current round — this test is now checking nothing")
	}

	snap, err = f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Round != nil || snap.Scoring != nil {
		t.Fatalf("the board phase carries a round (%v) or scoring (%v)", snap.Round, snap.Scoring)
	}
	last := snap.LastRound
	if last == nil {
		t.Fatal("the board phase has forgotten the round it just scored")
	}
	if last.AnswerValue != correct {
		t.Fatalf("last round answer = %v, want %v", last.AnswerValue, correct)
	}
	if last.Prompt == "" {
		t.Fatal("the last round carries no prompt")
	}
	if last.WinningSlotID == nil {
		t.Fatal("the exactly-right answer did not win a card")
	}
	if len(last.WinnerNames) != 1 || last.WinnerNames[0] != "Bar Flies" {
		t.Fatalf("winning card written by %v, want the table that was right", last.WinnerNames)
	}
	if len(last.WinnerIDs) != 1 || last.WinnerIDs[0] != a.ID {
		t.Fatalf("winner ids = %v, want %v — the name and the id must agree", last.WinnerIDs, a.ID)
	}
	if d := last.Deltas[a.ID]; d.BoardPoints != 500 {
		t.Fatalf("the winning table's board points = %+v, want 500", d)
	}

	// And the host frame carries it while the public frames do not.
	host := ProjectHost(snap)
	if host.LastRound == nil || host.LastRound.WinningLabel == "" {
		t.Fatalf("host frame last round = %+v", host.LastRound)
	}
}
