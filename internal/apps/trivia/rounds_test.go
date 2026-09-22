package trivia

import (
	"slices"
	"strings"
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

// Each round is worth one more than the one before -- CELLS AND CHIPS
// TOGETHER. Scaling only one of them is the bug this pins: scale the cells
// alone and knowing outruns reading the room, scale the chips alone and the
// board stops mattering.
func TestLaterRoundsScaleCellsAndChipsTogether(t *testing.T) {
	f := newFixture(t)
	f.seedBank(tenTopics(), 4)
	s := twoRoundSettings()
	game := f.newGame(s, nil)
	cells := f.buildRounds(game, tenTopics())

	for _, c := range cells {
		want := s.CellValues[0] * (c.RoundIndex + 1)
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

// Every field of a Game has to survive the trip out to the console and back,
// because the console PATCHes the whole settings object on any edit.
//
// board_rounds was dropped from a hand-written literal and the failure was
// nothing like "a field is missing": the picker read 0 rounds as 0 columns and
// refused every category tick on every game, and editing an unrelated timer
// silently collapsed a two-round night to one. This test is cheap insurance
// against the next field.
func TestSettingsSurviveTheRoundTrip(t *testing.T) {
	f := newFixture(t)
	want := Settings{
		Title: "Tuesday Quiz", BoardRows: 2, BoardColumns: 4,
		CellValues: []int{150, 150}, TokenValues: []int{100, 200},
		FinalWager: true, AnswerSeconds: 55, RevealSeconds: 10, BetSeconds: 40,
		WagerSeconds: 25, GraceSeconds: 3, RepeatQuestions: true, BoardRounds: 3,
	}
	name, err := UniqueName(f.ctx, f.pool, f.tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	game, err := CreateGame(f.ctx, f.pool, f.tenant.ID, name, want, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := SettingsOf(f.reload(game.ID)); !settingsEqual(got, want) {
		t.Fatalf("settings came back as %+v, want %+v", got, want)
	}

	// And the same object, sent straight back, must not change anything --
	// which is exactly what the console does on every edit.
	updated, err := UpdateSettings(f.ctx, f.pool, f.tenant.ID, game.ID, normaliseSettings(SettingsOf(game)))
	if err != nil {
		t.Fatal(err)
	}
	if got := SettingsOf(updated); !settingsEqual(got, want) {
		t.Fatalf("a no-op save changed the settings to %+v, want %+v", got, want)
	}
}

func settingsEqual(a, b Settings) bool {
	if a.Title != b.Title || a.BoardRows != b.BoardRows || a.BoardColumns != b.BoardColumns ||
		a.FinalWager != b.FinalWager || a.AnswerSeconds != b.AnswerSeconds ||
		a.RevealSeconds != b.RevealSeconds || a.BetSeconds != b.BetSeconds ||
		a.WagerSeconds != b.WagerSeconds || a.GraceSeconds != b.GraceSeconds ||
		a.RepeatQuestions != b.RepeatQuestions || a.BoardRounds != b.BoardRounds {
		return false
	}
	return slices.Equal(a.CellValues, b.CellValues) && slices.Equal(a.TokenValues, b.TokenValues)
}

// The wire's round number is what the TV and the host cue read out, and both
// of them added one to it on top of the one the projection already added --
// so a two-round night announced "Round 3 of 2" in front of the room.
func TestTheWireRoundNumberIsTheOneToReadOut(t *testing.T) {
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

	snap, err := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := ProjectHost(snap); got.BoardRound != 1 || got.BoardRounds != 2 {
		t.Fatalf("round one reports %d of %d, want 1 of 2", got.BoardRound, got.BoardRounds)
	}

	f.playNextCell(f.reload(game.ID), team)
	snap, err = f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	if err != nil {
		t.Fatal(err)
	}
	// At the break the wire already names the round ABOUT TO BE PLAYED, so a
	// surface reads it straight out with no arithmetic of its own.
	if got := ProjectHost(snap); got.BoardRound != 2 || got.BoardRounds != 2 {
		t.Fatalf("the break reports %d of %d, want 2 of 2", got.BoardRound, got.BoardRounds)
	}
}

// snap.Board is the round in play, so anything counting progress has to read
// the night-wide totals instead -- or the break reports "0 of 10 done" when
// half the night is behind you.
func TestProgressCountsTheNightNotTheRound(t *testing.T) {
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
	f.playNextCell(f.reload(game.ID), team)

	snap, err := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snap.CellsTotal != 2 || snap.CellsPlayed != 1 {
		t.Fatalf("progress is %d of %d, want 1 of 2 across the whole night",
			snap.CellsPlayed, snap.CellsTotal)
	}
	if got := phaseSentence(snap); !strings.Contains(got, "1 of 2") {
		t.Fatalf("phaseSentence = %q, want it to count the night", got)
	}
}

// The host decides at the bar, not at setup. A night that is going well has
// to be extendable without rebuilding the board the room is playing.
func TestTheHostCanAddARoundMidGame(t *testing.T) {
	f := newFixture(t)
	f.seedBank(tenTopics(), 2)
	s := twoRoundSettings()
	s.BoardColumns, s.BoardRows = 1, 1
	s.CellValues = []int{100}
	s.BoardRounds = 1
	// The final ON, because that is what leaves the night WAITING on an
	// emptied board -- which is the moment a host is asked "another one?".
	// With it off the game goes straight to the podium and there is no such
	// moment; see TestFinalWagerOffGoesStraightToPodium.
	s.FinalWager = true
	game := f.newGame(s, nil)
	f.buildRounds(game, tenTopics()[:1])
	team := f.join(game.ID, "Bar Flies")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})

	// One round, one cell: playing it empties the night, and with no final
	// the game would otherwise be over.
	f.playNextCell(f.reload(game.ID), team)

	if err := f.svc.AddBoardRound(f.ctx, f.tenant.ID, game.ID); err != nil {
		t.Fatalf("adding a round: %v", err)
	}
	cells, err := ListBoardCells(f.ctx, f.pool, f.tenant.ID, game.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := BoardRoundCount(cells); got != 2 {
		t.Fatalf("the night has %d rounds, want 2", got)
	}

	// The new board is the round in play, it is worth twice round one, and
	// the question it asks was not already asked tonight.
	snap, err := f.svc.Snapshot(f.ctx, f.tenant.ID, game.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Board) != 1 || snap.Board[0].Points != 200 {
		t.Fatalf("added board = %+v, want one cell worth 200", snap.Board)
	}
	if snap.TokenValues[0] != 200 {
		t.Fatalf("chips are %v, want them scaled with the cells", snap.TokenValues)
	}
	seen := map[uuid.UUID]bool{}
	for _, c := range cells {
		if seen[c.QuestionID] {
			t.Fatalf("the added round repeats a question from earlier tonight")
		}
		seen[c.QuestionID] = true
	}
	// And a fresh category, not a second helping of the first one.
	if cellTopicKey(cells[0]) == cellTopicKey(cells[1]) {
		t.Fatalf("the added round reuses category %q", cells[1].Topic)
	}
}

// A bank with nothing left has to say so in a sentence the host can act on,
// not fail silently at the moment they have told the room there is more.
func TestAddingARoundSaysSoWhenTheBankIsSpent(t *testing.T) {
	f := newFixture(t)
	// One category, and round one used it.
	f.seedBank([]string{"space"}, 2)
	s := twoRoundSettings()
	s.BoardColumns, s.BoardRows = 1, 1
	s.CellValues = []int{100}
	s.BoardRounds = 1
	s.FinalWager = true
	game := f.newGame(s, []string{"space"})
	f.join(game.ID, "Bar Flies")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})

	err := f.svc.AddBoardRound(f.ctx, f.tenant.ID, game.ID)
	if err == nil {
		t.Fatal("adding a round with no unused categories was allowed")
	}
	if !strings.Contains(err.Error(), "another round") {
		t.Fatalf("refusal is not actionable: %v", err)
	}
}
