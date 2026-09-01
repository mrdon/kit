package trivia

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// These are the cluster tests: what happens when N instances all run the
// 500ms sweeper against one database at the same moment.
//
// The design claim is that the sweeper is safe BY CONSTRUCTION rather than by
// coordination — no leader election, no advisory lock, no "only run this on
// instance 0". Every instance issues the same guarded conditional UPDATE as
// the first statement of a transaction, and the work that follows (building
// the reveal, scoring the round) rides inside that same transaction. Postgres
// serialises the racers on the row; at READ COMMITTED the losers re-evaluate
// their WHERE against the committed row, no longer match on `phase`, update
// zero rows, and return having done nothing.
//
// That claim is worth testing rather than asserting, because the cost of it
// being wrong is a double-scored round in front of a room.

// sweepRace fires n concurrent SweepDue calls, each through its own Service
// (a separate Service is what a separate instance has), and returns when they
// have all finished.
func (f *fixture) sweepRace(t *testing.T, gameID uuid.UUID, n int) {
	t.Helper()
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		svc := NewService(f.pool) // a distinct instance
		wg.Go(func() {
			<-start // release them together, to make the race as tight as possible
			errs[i] = svc.SweepDue(f.ctx, f.tenant.ID, gameID)
		})
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("instance %d: %v", i, err)
		}
	}
}

// expire drags the phase deadline into the past, which is what every instance
// then races to act on.
func (f *fixture) expire(t *testing.T, gameID uuid.UUID) {
	t.Helper()
	past := time.Now().UTC().Add(-10 * time.Second)
	if _, err := f.pool.Exec(f.ctx,
		`UPDATE app_trivia_games SET phase_deadline = $3 WHERE tenant_id = $1 AND id = $2`,
		f.tenant.ID, gameID, past); err != nil {
		t.Fatal(err)
	}
}

// Eight instances all notice the same expired answer clock. Exactly one
// transition must happen, and the reveal must be built exactly once — eight
// copies of every card would be visible to the whole room.
func TestClusterSweepAdvancesOnceAndBuildsOneReveal(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())
	a := f.join(game.ID, "Bar Flies")
	b := f.join(game.ID, "Quiz Khalifa")
	// A third table that never answers, so the phase is still open when the
	// clock runs out. With everyone in, the early close would have ended it
	// before the sweepers ever raced.
	f.join(game.ID, "The Quiet Ones")
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

	before := f.reload(game.ID)
	if before.Phase != PhaseQuestion {
		t.Fatalf("setup left the game in %s, want question", before.Phase)
	}
	f.expire(t, game.ID)

	f.sweepRace(t, game.ID, 8)

	after := f.reload(game.ID)
	if after.Phase != PhaseReveal {
		t.Fatalf("phase = %s after the race, want reveal", after.Phase)
	}
	// The version moves for the winning transition and for nothing else. Any
	// extra increment means another instance also did work.
	if got := after.StateVersion - before.StateVersion; got != 1 {
		t.Fatalf("state_version moved by %d, want exactly 1 — more than one instance transitioned", got)
	}
	// Two distinct answers plus the pseudo-slot. Eight would be eight reveals.
	slots, err := ListSlots(f.ctx, f.pool, f.tenant.ID, *after.CurrentRoundID)
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 3 {
		t.Fatalf("got %d cards after 8 instances raced, want 3 — the reveal was built more than once", len(slots))
	}
}

// The same race on the betting clock, which is the one that matters most: a
// round scored twice would pay every winning chip twice.
func TestClusterSweepScoresARoundExactlyOnce(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())
	a := f.join(game.ID, "Bar Flies")
	b := f.join(game.ID, "Quiz Khalifa")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})

	snap, _ := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	cellID := snap.Board[0].ID
	f.do(game.ID, ActionRequest{Action: ActionPickCell, FromPhase: PhaseBoard, CellID: &cellID})
	correct := snapCorrect(t, f, game)
	if err := f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, a.ID, FormatValue(correct), nil); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, b.ID, FormatValue(correct+1000), nil); err != nil {
		t.Fatal(err)
	}
	f.do(game.ID, ActionRequest{Action: ActionOpenBetting, FromPhase: PhaseReveal})

	snap, _ = f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	var winning uuid.UUID
	for _, sl := range snap.Slots {
		if sl.Value != nil && *sl.Value == correct {
			winning = sl.ID
		}
	}
	if err := f.svc.PlaceChip(f.ctx, f.tenant.ID, game.ID, b.ID, 1, &winning, 0); err != nil {
		t.Fatal(err)
	}

	before := f.reload(game.ID)
	f.expire(t, game.ID)
	f.sweepRace(t, game.ID, 8)

	after := f.reload(game.ID)
	if after.Phase != PhaseScoring {
		t.Fatalf("phase = %s, want scoring", after.Phase)
	}
	if got := after.StateVersion - before.StateVersion; got != 1 {
		t.Fatalf("state_version moved by %d, want exactly 1", got)
	}

	// The arithmetic is the real assertion: the writer of the winning answer
	// takes the cell ONCE and the winning chip pays ONCE.
	final, _ := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	if got := final.Standings[a.ID]; got != 500 {
		t.Fatalf("the team that wrote the winner has %d, want 500 — the round was scored more than once", got)
	}
	if got := final.Standings[b.ID]; got != 200 {
		t.Fatalf("the winning $200 chip paid %d, want 200 — the round was scored more than once", got)
	}
}

// Sweeping a game whose clock has NOT expired must do nothing at all, however
// many instances look at it. This is what stops a cluster from stampeding a
// game forward the moment it is created.
func TestClusterSweepIsANoOpBeforeTheDeadline(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())
	f.join(game.ID, "Bar Flies")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})
	snap, _ := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	cellID := snap.Board[0].ID
	f.do(game.ID, ActionRequest{Action: ActionPickCell, FromPhase: PhaseBoard, CellID: &cellID})

	before := f.reload(game.ID)
	f.sweepRace(t, game.ID, 8) // deadline is 60s away

	after := f.reload(game.ID)
	if after.Phase != before.Phase || after.StateVersion != before.StateVersion {
		t.Fatalf("a live game moved under 8 idle sweepers: %s v%d -> %s v%d",
			before.Phase, before.StateVersion, after.Phase, after.StateVersion)
	}
}

// A host clicking at the exact moment the clock runs out, against a cluster
// that is also sweeping. The click and the sweep must not both advance.
func TestClusterHostClickRacingTheSweepAdvancesOnce(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())
	a := f.join(game.ID, "Bar Flies")
	f.join(game.ID, "Quiz Khalifa")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})
	snap, _ := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	cellID := snap.Board[0].ID
	f.do(game.ID, ActionRequest{Action: ActionPickCell, FromPhase: PhaseBoard, CellID: &cellID})
	if err := f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, a.ID, "10", nil); err != nil {
		t.Fatal(err)
	}

	before := f.reload(game.ID)
	f.expire(t, game.ID)

	// The host's "Reveal answers" on one instance, seven sweepers on others.
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		<-start
		host := NewService(f.pool)
		_, _ = host.Do(f.ctx, f.tenant.ID, game.ID,
			ActionRequest{Action: ActionReveal, FromPhase: PhaseQuestion})
	})
	for range 7 {
		svc := NewService(f.pool)
		wg.Go(func() {
			<-start
			_ = svc.SweepDue(f.ctx, f.tenant.ID, game.ID)
		})
	}
	close(start)
	wg.Wait()

	after := f.reload(game.ID)
	if after.Phase != PhaseReveal {
		t.Fatalf("phase = %s, want reveal", after.Phase)
	}
	if got := after.StateVersion - before.StateVersion; got != 1 {
		t.Fatalf("state_version moved by %d, want exactly 1 — the click and a sweep both advanced", got)
	}
	slots, err := ListSlots(f.ctx, f.pool, f.tenant.ID, *after.CurrentRoundID)
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 2 {
		t.Fatalf("got %d cards, want 2 — the reveal was built more than once", len(slots))
	}
}
