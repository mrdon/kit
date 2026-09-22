package trivia

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func nowForTest() time.Time { return time.Now().UTC() }

// twoRoundSettings is the pub hour: two small boards and a final.
func twoRoundSettings() Settings {
	s := defaultSettings()
	s.BoardRounds = 2
	s.CellValues = []int{100, 100}
	s.TokenValues = []int{100, 200}
	return s
}

// buildRounds is the fixture's board build for a multi-round night: it picks
// its own categories, the way Auto does, because a two-round board needs
// twice as many and hand-listing them in every test is noise.
func (f *fixture) buildRounds(game *Game, topics []string) []BoardCell {
	f.t.Helper()
	keys := make([]string, len(topics))
	for i, t := range topics {
		keys[i] = FoldKey(t)
	}
	rounds := boardRoundsOf(game)
	taken := map[uuid.UUID]bool{}
	var all []BoardCell
	for round := range rounds {
		slice := keys[round*game.BoardColumns : (round+1)*game.BoardColumns]
		cells, err := drawRound(f.ctx, f.pool, f.tenant.ID, game, slice, nil, int64(round+1), round, taken)
		if err != nil {
			f.t.Fatalf("drawing round %d: %v", round, err)
		}
		for _, c := range cells {
			taken[c.QuestionID] = true
		}
		all = append(all, cells...)
	}
	if err := ReplaceBoard(f.ctx, f.pool, f.tenant.ID, game.ID, all); err != nil {
		f.t.Fatalf("writing a two-round board: %v", err)
	}
	return all
}

// tenTopics is enough distinct categories for two five-column rounds.
func tenTopics() []string {
	return []string{
		"space", "sports", "film", "food", "history",
		"music", "science", "art", "books", "animals",
	}
}

// A round is worth double the one before it -- CELLS AND CHIPS TOGETHER.
// Doubling only one of them is the bug this pins: double the cells alone and
// knowing outruns reading the room, double the chips alone and the board
// stops mattering.
func TestSecondRoundDoublesCellsAndChips(t *testing.T) {
	f := newFixture(t)
	f.seedBank(tenTopics(), 4)
	s := twoRoundSettings()
	game := f.newGame(s, nil)
	cells := f.buildRounds(game, tenTopics())

	for _, c := range cells {
		want := s.CellValues[0] * (1 << c.RoundIndex)
		if c.Points != want {
			t.Fatalf("a round-%d cell is worth %d, want %d", c.RoundIndex, c.Points, want)
		}
	}

	// The chips the phone is offered follow the same multiplier, and they are
	// read off the snapshot, which is the only number a player ever sees.
	snap, err := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := snap.TokenValues; got[0] != 100 || got[1] != 200 {
		t.Fatalf("round one chips are %v, want the unscaled 100/200", got)
	}
}

// Every round gets its own categories. Two rounds on the same five columns
// would read as the same board played twice.
func TestEachRoundHasItsOwnCategories(t *testing.T) {
	f := newFixture(t)
	f.seedBank(tenTopics(), 4)
	game := f.newGame(twoRoundSettings(), nil)
	cells := f.buildRounds(game, tenTopics())

	seen := map[int]map[string]bool{}
	for _, c := range cells {
		if seen[c.RoundIndex] == nil {
			seen[c.RoundIndex] = map[string]bool{}
		}
		seen[c.RoundIndex][FoldKey(c.Topic)] = true
	}
	for topic := range seen[0] {
		if seen[1][topic] {
			t.Fatalf("category %q is on both rounds", topic)
		}
	}
	// And no question is asked twice across the night, which the bank query
	// cannot enforce on its own: nothing is written until every round is drawn.
	qs := map[uuid.UUID]bool{}
	for _, c := range cells {
		if qs[c.QuestionID] {
			t.Fatalf("question %s is on the board twice", c.QuestionID)
		}
		qs[c.QuestionID] = true
	}
}

// The room sees the board it is playing and no other. Without this the TV
// shows both rounds at once and a phone could pick into a board the night has
// not reached.
func TestOnlyTheRoundInPlayIsProjected(t *testing.T) {
	f := newFixture(t)
	f.seedBank(tenTopics(), 4)
	game := f.newGame(twoRoundSettings(), nil)
	all := f.buildRounds(game, tenTopics())

	snap, err := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Board) != len(all)/2 {
		t.Fatalf("the snapshot carries %d cells of %d, want only round one", len(snap.Board), len(all))
	}
	if snap.BoardRounds != 2 || snap.BoardRound != 0 {
		t.Fatalf("snapshot says round %d of %d, want 0 of 2", snap.BoardRound, snap.BoardRounds)
	}
}

// CurrentBoardRound is derived, never stored, so it cannot drift from the
// board the room is looking at.
func TestCurrentBoardRoundFollowsThePlayedCells(t *testing.T) {
	played := func(round int, done bool) BoardCell {
		c := BoardCell{RoundIndex: round}
		if done {
			now := nowForTest()
			c.PlayedAt = &now
		}
		return c
	}
	cells := []BoardCell{played(0, true), played(0, false), played(1, false)}
	if got := CurrentBoardRound(cells); got != 0 {
		t.Fatalf("round = %d, want 0 while round one still has a cell", got)
	}
	cells[1] = played(0, true)
	if got := CurrentBoardRound(cells); got != 1 {
		t.Fatalf("round = %d, want 1 once round one is spent", got)
	}
	cells[2] = played(1, true)
	if got := CurrentBoardRound(cells); got != 2 {
		t.Fatalf("round = %d, want the round count once the board is spent", got)
	}
}

// playNextCell takes the next unplayed cell of the round in play all the way
// through to scored, which is the loop a round is made of. It reads the cell
// off the PROJECTED board on purpose: that is the only one the room can see,
// so a bug that offered a later round's cell would fail here.
func (f *fixture) playNextCell(game *Game, team *Team) {
	f.t.Helper()
	snap, err := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	var cellID uuid.UUID
	for _, c := range snap.Board {
		if !c.Played {
			cellID = c.ID
			break
		}
	}
	if cellID == uuid.Nil {
		f.t.Fatal("no unplayed cell in the projected board")
	}
	f.do(game.ID, ActionRequest{Action: ActionPickCell, FromPhase: PhaseBoard, CellID: &cellID})
	if err := f.svc.SubmitAnswer(f.ctx, f.tenant.ID, game.ID, team.ID, "1"); err != nil {
		f.t.Fatalf("answering: %v", err)
	}
	f.do(game.ID, ActionRequest{Action: ActionOpenBetting, FromPhase: PhaseReveal})
	f.do(game.ID, ActionRequest{Action: ActionScore, FromPhase: PhaseBetting})
	f.do(game.ID, ActionRequest{Action: ActionNext, FromPhase: PhaseScoring})
}

// The seam in the night. A game that rolls straight from one board into the
// next never gives the room the break that is the whole reason for a second
// board, so the end of a round is a PHASE, not a transition.
func TestAFinishedRoundStopsAtTheIntermission(t *testing.T) {
	f := newFixture(t)
	f.seedBank([]string{"space", "sports"}, 2)
	s := twoRoundSettings()
	s.BoardColumns, s.BoardRows = 1, 1
	s.CellValues = []int{100}
	s.FinalWager = false
	game := f.newGame(s, nil)
	f.buildRounds(game, []string{"space", "sports"})
	team := f.join(game.ID, "Bar Flies")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})

	// Round one is one cell long, so playing it empties the round.
	f.playNextCell(f.reload(game.ID), team)
	if got := f.reload(game.ID).Phase; got != PhaseIntermission {
		t.Fatalf("phase after round one = %q, want the intermission", got)
	}

	// Nothing is on a clock during the break -- the host says when the room
	// is back.
	if f.reload(game.ID).PhaseDeadline != nil {
		t.Fatal("the intermission is running a countdown; it waits on a human")
	}

	// And the room is now looking at round two, worth double.
	f.do(game.ID, ActionRequest{Action: ActionResume, FromPhase: PhaseIntermission})
	reloaded := f.reload(game.ID)
	if reloaded.Phase != PhaseBoard {
		t.Fatalf("phase after the break = %q, want the board", reloaded.Phase)
	}
	snap, err := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Board) != 1 || snap.Board[0].Points != 200 {
		t.Fatalf("round two board = %+v, want one cell worth 200", snap.Board)
	}
	if snap.TokenValues[0] != 200 || snap.TokenValues[1] != 400 {
		t.Fatalf("round two chips are %v, want them doubled to 200/400", snap.TokenValues)
	}

	// Playing round two empties the night, and with no final that is the podium.
	f.playNextCell(reloaded, team)
	if got := f.reload(game.ID).Phase; got != PhasePodium {
		t.Fatalf("phase after the last round = %q, want the podium", got)
	}
}

// A single-round night must be exactly the game as it shipped: no break, no
// doubling, straight from the emptied board to the end.
func TestASingleRoundNightNeverBreaks(t *testing.T) {
	f := newFixture(t)
	f.seedBank([]string{"space"}, 2)
	s := defaultSettings()
	s.BoardColumns, s.BoardRows = 1, 1
	s.CellValues = []int{100}
	s.FinalWager = false
	game := f.newGame(s, []string{"space"})
	team := f.join(game.ID, "Bar Flies")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})

	f.playNextCell(f.reload(game.ID), team)
	if got := f.reload(game.ID).Phase; got != PhasePodium {
		t.Fatalf("a one-round night ended in %q, want the podium with no break", got)
	}
}
