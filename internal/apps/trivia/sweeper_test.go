package trivia

import (
	"context"
	"testing"
	"time"
)

// The sweeper is deadline-driven, not a ticker: armed by a publish, it sleeps
// until the clock runs out and closes the phase with nobody touching the game.
func TestSweeperClosesPhaseWhenItsDeadlineArrives(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())
	f.join(game.ID, "Bar Flies")
	f.join(game.ID, "Quiz Khalifa")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})
	snap, _ := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	cellID := snap.Board[0].ID
	f.do(game.ID, ActionRequest{Action: ActionPickCell, FromPhase: PhaseBoard, CellID: &cellID})

	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	f.svc.StartSweeper(ctx)

	// Pull the clock in to just ahead of now, then publish -- the same signal
	// every deadline-moving write sends -- so the sleeping sweeper re-arms.
	soon := time.Now().UTC().Add(300 * time.Millisecond)
	if _, err := f.pool.Exec(f.ctx,
		`UPDATE app_trivia_games SET phase_deadline = $3 WHERE tenant_id = $1 AND id = $2`,
		f.tenant.ID, game.ID, soon); err != nil {
		t.Fatal(err)
	}
	f.svc.publish(f.ctx, f.tenant.ID, game.ID)

	giveUp := time.Now().Add(10 * time.Second)
	for time.Now().Before(giveUp) {
		if g := f.reload(game.ID); g.Phase == PhaseReveal {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("phase = %s 10s after the deadline, want reveal: the sweeper never woke", f.reload(game.ID).Phase)
}

func TestNextDeadlineInSeesARunningClock(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())
	// A lobby has no clock; other tests' games may, so only assert the
	// call works and that a future deadline we set is seen as future.
	if _, _, err := NextDeadlineIn(f.ctx, f.pool); err != nil {
		t.Fatal(err)
	}
	far := time.Now().UTC().Add(time.Hour)
	if _, err := f.pool.Exec(f.ctx,
		`UPDATE app_trivia_games SET phase_deadline = $3 WHERE tenant_id = $1 AND id = $2`,
		f.tenant.ID, game.ID, far); err != nil {
		t.Fatal(err)
	}
	wait, ok, err := NextDeadlineIn(f.ctx, f.pool)
	if err != nil || !ok {
		t.Fatalf("NextDeadlineIn = %v, %v, %v; want a deadline", wait, ok, err)
	}
	if wait > time.Hour+graceWindow+time.Second {
		t.Fatalf("wait = %v, longer than the deadline we set", wait)
	}
}

func TestBrokerChangedCoalesces(t *testing.T) {
	b := NewBroker()
	snap := &Snapshot{}
	b.Publish(snap.GameID, snap)
	b.Publish(snap.GameID, snap) // must not block with a signal already pending
	select {
	case <-b.Changed():
	default:
		t.Fatal("Publish did not signal Changed")
	}
	select {
	case <-b.Changed():
		t.Fatal("two publishes left two signals; want one")
	default:
	}
}
