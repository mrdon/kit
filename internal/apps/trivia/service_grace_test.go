package trivia

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// graceSettings is the fixture board with a grace on it. The shared
// defaultSettings() deliberately leaves GraceSeconds at zero so every test
// written before the grace existed still sees the instant close it asserts;
// these tests opt in.
func graceSettings(sec int) Settings {
	s := defaultSettings()
	s.GraceSeconds = sec
	return s
}

// The answer clock does not vanish out from under the table that answered
// last: it drops to the grace, in public, on the countdown everyone is
// already watching.
func TestEveryoneAnsweringShortensTheClockInsteadOfClosing(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(graceSettings(5), topicSet())
	a := f.join(game.ID, "Bar Flies")
	b := f.join(game.ID, "Quiz Khalifa")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})

	snap, _ := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	cellID := snap.Board[0].ID
	f.do(game.ID, ActionRequest{Action: ActionPickCell, FromPhase: PhaseBoard, CellID: &cellID})

	opened := f.reload(game.ID)
	longClock := *opened.PhaseDeadline

	if err := f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, a.ID, "10"); err != nil {
		t.Fatal(err)
	}
	if g := f.reload(game.ID); *g.PhaseDeadline != longClock {
		t.Fatal("the clock moved with one table still to answer")
	}
	if err := f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, b.ID, "20"); err != nil {
		t.Fatal(err)
	}

	g := f.reload(game.ID)
	if g.Phase != PhaseQuestion {
		t.Fatalf("phase = %s once everyone was in; with a grace the question stays open", g.Phase)
	}
	left := time.Until(*g.PhaseDeadline)
	if left <= 0 || left > 5*time.Second {
		t.Fatalf("%v left on the clock, want something inside the 5s grace", left)
	}
	if !g.PhaseDeadline.Before(longClock) {
		t.Fatal("the deadline was not pulled in at all")
	}
	// The shortening has to reach the room, which means a new frame id.
	if g.StateVersion <= opened.StateVersion {
		t.Fatal("the shortened clock did not bump state_version, so no surface repaints")
	}

	// And the ordinary timer path -- the same one the sweeper, the ticker and
	// the scheduled backstop share -- is what actually ends it.
	f.expire(t, game.ID)
	if err := f.svc.SweepDue(f.ctx, f.tenant.ID, game.ID); err != nil {
		t.Fatal(err)
	}
	if after := f.reload(game.ID); after.Phase != PhaseReveal {
		t.Fatalf("phase = %s after the shortened clock ran out, want reveal", after.Phase)
	}
}

// Grace 0 is the old behaviour, kept as a setting rather than deleted: a host
// who wants the night to move can still have the last chip end the phase.
func TestGraceOfZeroStillClosesTheInstantEveryoneIsIn(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(graceSettings(0), topicSet())
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
	if g := f.reload(game.ID); g.Phase != PhaseReveal {
		t.Fatalf("phase = %s with grace 0, want the reveal it always went to", g.Phase)
	}
}

// The grace is a beat, not a stay of execution. A table that spends it moving
// a chip -- which is exactly what it is for -- must not push the deadline
// back out, or one twitchy thumb holds the whole room indefinitely.
func TestMovingAChipDuringTheGraceDoesNotReArmIt(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(graceSettings(5), topicSet())
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
	f.do(game.ID, ActionRequest{Action: ActionReveal, FromPhase: PhaseQuestion})
	f.do(game.ID, ActionRequest{Action: ActionOpenBetting, FromPhase: PhaseReveal})

	snap, _ = f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	slots := snap.Slots
	place := func(team uuid.UUID, chip int, slot uuid.UUID) {
		t.Helper()
		if err := f.svc.PlaceChip(f.ctx, f.tenant.ID, game.ID, team, chip, &slot, 0); err != nil {
			t.Fatalf("placing chip: %v", err)
		}
	}
	place(a.ID, 0, slots[1].ID)
	place(a.ID, 1, slots[2].ID)
	place(b.ID, 0, slots[0].ID)
	place(b.ID, 1, slots[1].ID)

	shortened := f.reload(game.ID)
	if shortened.Phase != PhaseBetting {
		t.Fatalf("phase = %s once every chip was down; with a grace betting stays open", shortened.Phase)
	}
	if left := time.Until(*shortened.PhaseDeadline); left <= 0 || left > 5*time.Second {
		t.Fatalf("%v left on the betting clock, want something inside the 5s grace", left)
	}

	// B uses the grace for the thing the grace exists for.
	place(b.ID, 1, slots[2].ID)
	after := f.reload(game.ID)
	if after.Phase != PhaseBetting {
		t.Fatalf("phase = %s after a chip moved inside the grace, want betting", after.Phase)
	}
	if !after.PhaseDeadline.Equal(*shortened.PhaseDeadline) {
		t.Fatalf("the deadline moved from %v to %v — the grace was re-armed",
			shortened.PhaseDeadline, after.PhaseDeadline)
	}
	// The move itself landed, which is the whole point of giving them the beat.
	snap, _ = f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	if got := chipSlot(t, snap, b.ID, 1); got != slots[2].ID {
		t.Fatalf("B's second chip is on %v, want the card it moved to", got)
	}
}

// The final's blind bet gets the beat too: the table that locks last still
// sees the room go quiet before the question arrives.
func TestTheWagerClockShortensWhenEveryTableHasLocked(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	s := oneCellSettings()
	s.GraceSeconds = 5
	// The shared fixture predates the wager phase and leaves its clock at
	// zero, which would arm a deadline already in the past.
	s.WagerSeconds = 30
	game := f.newGame(s, []string{"space"})
	a := f.join(game.ID, "Bar Flies")
	b := f.join(game.ID, "Quiz Khalifa")
	f.openFinal(game, a, b)

	if err := f.svc.SetWager(f.ctx, f.tenant.ID, game.ID, a.ID, 100); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.SetWager(f.ctx, f.tenant.ID, game.ID, b.ID, 0); err != nil {
		t.Fatal(err)
	}
	g := f.reload(game.ID)
	if g.Phase != PhaseWager {
		t.Fatalf("phase = %s once both tables had locked; with a grace the wager stays open", g.Phase)
	}
	if left := time.Until(*g.PhaseDeadline); left <= 0 || left > 5*time.Second {
		t.Fatalf("%v left on the wager clock, want something inside the 5s grace", left)
	}

	f.expire(t, game.ID)
	if err := f.svc.SweepDue(f.ctx, f.tenant.ID, game.ID); err != nil {
		t.Fatal(err)
	}
	after := f.reload(game.ID)
	if after.Phase != PhaseQuestion {
		t.Fatalf("phase = %s after the shortened wager clock ran out, want question", after.Phase)
	}
}

// chipSlot reads back where one team's chip actually sits.
func chipSlot(t *testing.T, snap *Snapshot, teamID uuid.UUID, tokenIndex int) uuid.UUID {
	t.Helper()
	for _, bet := range snap.Bets {
		if bet.TeamID == teamID && bet.TokenIndex == tokenIndex {
			return bet.SlotID
		}
	}
	t.Fatalf("no chip %d for team %v anywhere on the board", tokenIndex, teamID)
	return uuid.Nil
}

// The grace is a real setting with a real range, and zero has to stay legal
// inside it -- it is the behaviour the game shipped with.
func TestGraceValidationAllowsZeroAndBoundsTheTop(t *testing.T) {
	s := DefaultSettings()
	for _, v := range []int{0, 1, 5, 60} {
		s.GraceSeconds = v
		if err := validateSettings(s); err != nil {
			t.Fatalf("grace %d rejected: %v", v, err)
		}
	}
	for _, v := range []int{-1, 61} {
		s.GraceSeconds = v
		if err := validateSettings(s); err == nil {
			t.Fatalf("grace %d accepted, want a refusal", v)
		}
	}
}
