package trivia

import (
	"slices"
	"testing"
)

// Two tables writing the same number is the case the whole design leans on,
// and BuildSlots/ScoreRound cover it in isolation. What those cannot show is
// that a real round SURVIVES it end to end: nothing in the answers table, the
// slot write, the projections or the bet path may treat a shared value as a
// collision.
//
// If any of them did, the failure would arrive in front of a room -- two
// identical cards on the TV, or one table's answer silently replacing
// another's -- so it is worth a test that goes through the service.
func TestTwoTeamsCanWriteTheSameGuess(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())
	a := f.join(game.ID, "Bar Flies")
	b := f.join(game.ID, "Quiz Khalifa")
	c := f.join(game.ID, "The Quizzly Bears")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})
	snap, _ := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	cellID := snap.Board[0].ID
	f.do(game.ID, ActionRequest{Action: ActionPickCell, FromPhase: PhaseBoard, CellID: &cellID})

	// Guesses are pitched off the real answer so the shared card is the one
	// that WINS -- closest without going over -- rather than everybody
	// overshooting onto the pseudo-slot, which would prove nothing.
	snap, _ = f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	if snap.Round == nil {
		t.Fatal("no round in play after picking a cell")
	}
	near := snap.Round.CorrectValue - 1

	// Two tables land on the same number; one writes it with a decimal point,
	// which is the same number and must not become a second card.
	for _, sub := range []struct {
		team *Team
		raw  string
	}{
		{a, FormatValue(near)},
		{b, FormatValue(near) + ".0"},
		{c, FormatValue(near - 10)},
	} {
		if err := f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, sub.team.ID, sub.raw); err != nil {
			t.Fatalf("%s writing %q was refused: %v", sub.team.Name, sub.raw, err)
		}
	}
	f.do(game.ID, ActionRequest{Action: ActionOpenBetting, FromPhase: PhaseReveal})
	snap, _ = f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)

	// The pseudo-slot plus two cards, not three: the room sees one 1969.
	if len(snap.Slots) != 3 {
		labels := make([]string, 0, len(snap.Slots))
		for _, s := range snap.Slots {
			labels = append(labels, s.Label)
		}
		t.Fatalf("the room got %d cards %v, want the pseudo-slot and two", len(snap.Slots), labels)
	}
	// Ascending, so the shared card is the higher of the two.
	shared := snap.Slots[2]
	if len(shared.TeamIDs) != 2 {
		t.Fatalf("the shared card carries %d teams, want both", len(shared.TeamIDs))
	}
	if !slices.Contains(shared.TeamIDs, a.ID) || !slices.Contains(shared.TeamIDs, b.ID) {
		t.Fatalf("the shared card names %v, want %s and %s", shared.TeamNames, a.Name, b.Name)
	}

	// Both of them, and the table that wrote something else, can put a chip
	// on that same card. Bets are unique per (team, slot), never per slot.
	for _, team := range []*Team{a, b, c} {
		if err := f.svc.PlaceChip(f.ctx, f.tenant.ID, game.ID, team.ID, 0, &shared.ID, 0); err != nil {
			t.Fatalf("%s betting on the shared card was refused: %v", team.Name, err)
		}
	}

	// The shared card is nearest without going over, so it wins: both tables
	// take the FULL cell value -- splitting it would punish agreement -- and
	// all three chips pay.
	f.do(game.ID, ActionRequest{Action: ActionScore, FromPhase: PhaseBetting})
	snap, _ = f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	if snap.Scoring == nil {
		t.Fatal("the round did not score")
	}
	cellPoints := defaultSettings().CellValues[0]
	for _, team := range []*Team{a, b} {
		if got := snap.Scoring.Deltas[team.ID].BoardPoints; got != cellPoints {
			t.Fatalf("%s took %d board points, want the full %d", team.Name, got, cellPoints)
		}
	}
	if got := snap.Scoring.Deltas[c.ID].BoardPoints; got != 0 {
		t.Fatalf("%s wrote a different number and took %d board points, want 0", c.Name, got)
	}
	chip := defaultSettings().TokenValues[0]
	for _, team := range []*Team{a, b, c} {
		if got := snap.Scoring.Deltas[team.ID].BetDelta; got != chip {
			t.Fatalf("%s's chip on the winning card paid %d, want %d", team.Name, got, chip)
		}
	}
}
