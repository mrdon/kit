package trivia

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// snapshotIn builds a fully-populated snapshot sitting in the given phase,
// with an answer distinctive enough that finding it in a byte slice is
// unambiguous.
// Every id here is hex letters only and the clock is all ones, deliberately:
// the leak check is a substring search over the marshalled frame, and a
// random UUID that happened to contain the answer's digits would make this
// test fail for a reason that has nothing to do with a leak.
func fixedID(a, b, c, d, e string) uuid.UUID {
	return uuid.MustParse(a + "-" + b + "-" + c + "-" + d + "-" + e)
}

func snapshotIn(phase Phase, scored bool) (*Snapshot, uuid.UUID) {
	teamA := fixedID("aaaaaaaa", "aaaa", "aaaa", "aaaa", "aaaaaaaaaaaa")
	teamB := fixedID("bbbbbbbb", "bbbb", "bbbb", "bbbb", "bbbbbbbbbbbb")
	slotA := fixedID("cccccccc", "cccc", "cccc", "cccc", "cccccccccccc")
	slotB := fixedID("dddddddd", "dddd", "dddd", "dddd", "dddddddddddd")
	v1, v2 := 314159.0, 271828.0
	winning := slotA

	s := &Snapshot{
		GameID:   fixedID("eeeeeeee", "eeee", "eeee", "eeee", "eeeeeeeeeeee"),
		TenantID: fixedID("ffffffff", "ffff", "ffff", "ffff", "ffffffffffff"),
		Name:     "brave-otter-lamp", Title: "Tuesday Quiz", Phase: phase,
		StateVersion: 12, ServerNow: time.UnixMilli(1111111111111).UTC(),
		FinalWager: true, TokenValues: []int{100, 200}, CellValues: []int{500, 1000},
		BoardRows: 2, BoardCols: 5,
		Teams: []SnapTeam{
			{ID: teamA, Name: "Bar Flies", Score: 1500, Eligible: true, Answered: true, StakeLocked: true},
			{ID: teamB, Name: "Quiz Khalifa", Score: 900, Eligible: true},
		},
		Board: []SnapCell{{ID: fixedID("abababab", "abab", "abab", "abab", "abababababab"), Col: 0, Row: 0, Topic: "Space", Points: 500}},
		Round: &SnapRound{
			ID: fixedID("cdcdcdcd", "cdcd", "cdcd", "cdcd", "cdcdcdcdcdcd"), Ordinal: 3, Points: 500,
			Text:         "How many metres tall is the Eiffel Tower?",
			Topic:        "Landmarks",
			CorrectValue: 867530.0, CorrectText: "867530",
			AnsweredCount: 1, EligibleCount: 2,
		},
		Slots: []SnapSlot{
			{ID: slotA, Position: 1, Value: &v2, Label: "271828", TeamIDs: []uuid.UUID{teamA}, TeamNames: []string{"Bar Flies"}, Pot: 200},
			{ID: slotB, Position: 2, Value: &v1, Label: "314159", TeamIDs: []uuid.UUID{teamB}, TeamNames: []string{"Quiz Khalifa"}},
		},
		Bets:      []SnapBet{{TeamID: teamA, TokenIndex: 1, Amount: 200, SlotID: slotA}},
		Standings: map[uuid.UUID]int{teamA: 1500, teamB: 900},
	}
	if scored {
		s.Scoring = &SnapScoring{
			CorrectValue: 867530.0, CorrectText: "867530", WinningSlotID: &winning,
			Deltas: map[uuid.UUID]ScoreDelta{teamA: {BoardPoints: 500, BetDelta: 200}},
		}
	}
	return s, teamA
}

// TestProjectionsNeverLeakTheAnswer marshals both public projections for a
// snapshot in every pre-scoring phase and asserts the correct answer's digits
// appear nowhere in the bytes. The answer lives in Snapshot by necessity;
// this is the only thing standing between it and twenty phones.
func TestProjectionsNeverLeakTheAnswer(t *testing.T) {
	const answer = "867530"
	preScoring := []Phase{PhaseSetup, PhaseLobby, PhaseBoard, PhaseWager,
		PhaseQuestion, PhaseReveal, PhaseBetting}

	for _, phase := range preScoring {
		snap, teamID := snapshotIn(phase, false)

		display, err := json.Marshal(ProjectDisplay(snap))
		if err != nil {
			t.Fatalf("%s: marshalling display: %v", phase, err)
		}
		if strings.Contains(string(display), answer) {
			t.Fatalf("%s: the TV frame contains the answer %s:\n%s", phase, answer, display)
		}

		for label, id := range map[string]uuid.UUID{"player": teamID, "spectator": uuid.Nil} {
			player, err := json.Marshal(ProjectPlayer(snap, id))
			if err != nil {
				t.Fatalf("%s/%s: marshalling player: %v", phase, label, err)
			}
			if strings.Contains(string(player), answer) {
				t.Fatalf("%s: the %s frame contains the answer %s:\n%s", phase, label, answer, player)
			}
		}
	}
}

// The other half of the contract: once the round IS scored, both public
// surfaces get the answer. A withholding bug that never released it would be
// just as broken, and less obvious.
func TestProjectionsReleaseTheAnswerOnceScored(t *testing.T) {
	snap, teamID := snapshotIn(PhaseScoring, true)

	display, _ := json.Marshal(ProjectDisplay(snap))
	if !strings.Contains(string(display), "867530") {
		t.Fatalf("scored TV frame is missing the answer:\n%s", display)
	}
	player, _ := json.Marshal(ProjectPlayer(snap, teamID))
	if !strings.Contains(string(player), "867530") {
		t.Fatalf("scored player frame is missing the answer:\n%s", player)
	}
}

// The host reads the question out and adjudicates nothing, so the console
// carries the answer in every phase. Hiding it there would be theatre with a
// cost.
func TestHostProjectionAlwaysCarriesTheAnswer(t *testing.T) {
	for _, phase := range []Phase{PhaseQuestion, PhaseReveal, PhaseBetting} {
		snap, _ := snapshotIn(phase, false)
		frame := ProjectHost(snap)
		if frame.Answer == nil || frame.Answer.Text != "867530" {
			t.Fatalf("%s: host frame answer = %+v", phase, frame.Answer)
		}
	}
}

// The board phase clears the current round on purpose, and that is exactly
// when the host has to say who picks the next category. The host frame
// remembers the last scored round; the public frames are not widened.
func TestHostFrameRemembersTheLastScoredRound(t *testing.T) {
	fresh, _ := snapshotIn(PhaseBoard, false)
	fresh.Round, fresh.Scoring = nil, nil
	if got := ProjectHost(fresh).LastRound; got != nil {
		t.Fatalf("nothing has been scored yet but the host frame carries %+v", got)
	}

	snap, teamA := snapshotIn(PhaseBoard, false)
	slot := fixedID("cccccccc", "cccc", "cccc", "cccc", "cccccccccccc")
	value := 271828.0
	// What the board phase actually looks like: no round in play, but a
	// scored one behind it.
	snap.Round, snap.Scoring, snap.Slots = nil, nil, nil
	snap.LastRound = &SnapLastRound{
		LastRoundSummary: LastRoundSummary{
			RoundID: fixedID("cdcdcdcd", "cdcd", "cdcd", "cdcd", "cdcdcdcdcdcd"),
			Ordinal: 3, Points: 500, Prompt: "How many metres tall is the Eiffel Tower?",
			AnswerValue: 867530.0, AnswerText: "867530",
			WinningSlotID: &slot, WinningLabel: "271828", WinningValue: &value,
			WinnerIDs: []uuid.UUID{teamA}, WinnerNames: []string{"Bar Flies"},
		},
		Deltas: map[uuid.UUID]ScoreDelta{teamA: {BoardPoints: 500, BetDelta: 200}},
	}

	host := ProjectHost(snap)
	if host.LastRound == nil {
		t.Fatal("the host frame has forgotten the round it just scored")
	}
	if host.LastRound.WinningLabel != "271828" || len(host.LastRound.Winners) != 1 ||
		host.LastRound.Winners[0] != "Bar Flies" {
		t.Fatalf("winning card = %q by %v", host.LastRound.WinningLabel, host.LastRound.Winners)
	}
	if host.LastRound.WinningSlot != slot.String() ||
		len(host.LastRound.WinnerIDs) != 1 || host.LastRound.WinnerIDs[0] != teamA.String() {
		t.Fatalf("winning slot/team ids = %q %v", host.LastRound.WinningSlot, host.LastRound.WinnerIDs)
	}
	if host.LastRound.Deltas[teamA.String()] != 700 ||
		host.LastRound.BoardPoints[teamA.String()] != 500 ||
		host.LastRound.BetDeltas[teamA.String()] != 200 {
		t.Fatalf("last round deltas = %+v", host.LastRound)
	}
	if host.LastRound.CorrectText != "867530" {
		t.Fatalf("the host cannot read back the answer: %+v", host.LastRound)
	}

	// And it stays host-only: the TV and the phones have moved on.
	display, _ := json.Marshal(ProjectDisplay(snap))
	player, _ := json.Marshal(ProjectPlayer(snap, teamA))
	for label, raw := range map[string][]byte{"TV": display, "phone": player} {
		if strings.Contains(string(raw), "lastRound") {
			t.Fatalf("the %s frame carries the last round:\n%s", label, raw)
		}
	}
}

// Cards must not carry values or team names before the reveal: a phone that
// could read the field early would know exactly what to bet on.
func TestPublicSlotsAreEmptyBeforeReveal(t *testing.T) {
	for _, phase := range []Phase{PhaseBoard, PhaseQuestion} {
		snap, teamID := snapshotIn(phase, false)
		if got := len(ProjectDisplay(snap).Slots); got != 0 {
			t.Fatalf("%s: TV frame carries %d cards before the reveal", phase, got)
		}
		if got := len(ProjectPlayer(snap, teamID).Slots); got != 0 {
			t.Fatalf("%s: player frame carries %d cards before the reveal", phase, got)
		}
	}
	snap, _ := snapshotIn(PhaseReveal, false)
	if got := len(ProjectDisplay(snap).Slots); got != 2 {
		t.Fatalf("reveal frame carries %d cards, want 2", got)
	}
	if names := ProjectDisplay(snap).Slots[0].Teams; len(names) != 1 || names[0] != "Bar Flies" {
		t.Fatalf("revealed card team names = %v, want the writing team", names)
	}
}

// finalWithStake puts the snapshot in a final with team A's wager locked in.
// 7400 is deliberately a number that appears nowhere else in the fixture --
// the leak checks below are substring searches over the marshalled frames.
func finalWithStake(phase Phase) (*Snapshot, uuid.UUID, int) {
	snap, teamA := snapshotIn(phase, false)
	snap.Round.IsFinal = true
	stake := 7400
	snap.Teams[0].StakeLocked = true
	snap.Teams[0].Stake = &stake
	return snap, teamA, stake
}

// The wager phase's ONE rule: the room bets against a category, and the
// prompt does not exist on any public surface until the phase closes.
//
// A substring search over the marshalled frame rather than a field check,
// because the way this breaks is somebody widening a public struct later and
// the prompt riding out on a field nobody was thinking about.
func TestWagerPhaseCarriesTheCategoryAndNotTheQuestion(t *testing.T) {
	snap, teamA, _ := finalWithStake(PhaseWager)
	const prompt = "Eiffel Tower"

	frames := map[string]any{
		"the TV":      ProjectDisplay(snap),
		"the phone":   ProjectPlayer(snap, teamA),
		"a spectator": ProjectPlayer(snap, uuid.Nil),
	}
	for label, frame := range frames {
		raw, err := json.Marshal(frame)
		if err != nil {
			t.Fatalf("marshalling %s: %v", label, err)
		}
		if strings.Contains(string(raw), prompt) {
			t.Fatalf("%s carries the final's question during the wager:\n%s", label, raw)
		}
		if !strings.Contains(string(raw), "Landmarks") {
			t.Fatalf("%s has no category to bet against:\n%s", label, raw)
		}
	}

	// And the round itself is present -- the wager screen needs its ordinal
	// and its eligible count -- with the text field simply empty.
	r := ProjectDisplay(snap).Round
	if r == nil || r.Category != "Landmarks" || r.Text != "" || !r.IsFinal {
		t.Fatalf("wager round = %+v, want the category with no text", r)
	}

	// One phase later the prompt is public, on the same snapshot.
	snap.Phase = PhaseQuestion
	if got := ProjectDisplay(snap).Round; got == nil || !strings.Contains(got.Text, prompt) {
		t.Fatalf("the question never appeared once the wager closed: %+v", got)
	}
}

// The final's tension is that nobody knows whether the leader defended or sat
// out. The TV shows LOCKED, never the amount, until scoring.
func TestPublicFramesShowStakeLockedWithoutTheAmount(t *testing.T) {
	snap, _, stake := finalWithStake(PhaseQuestion)

	frame := ProjectDisplay(snap)
	if !frame.Teams[0].StakeLocked {
		t.Fatal("TV frame does not show the stake as locked")
	}
	raw, _ := json.Marshal(frame)
	if strings.Contains(string(raw), "7400") {
		t.Fatalf("the stake amount %d appears in the TV frame:\n%s", stake, raw)
	}
}

// The wager is the one number a table may see about ITSELF and nobody may see
// about anyone else. It has to reach the phone that staked it -- in betting
// that phone's single chip IS the stake, and with nothing to render it the
// chip read $0 -- and it must reach no other surface.
func TestOwnStakeReachesOnlyTheTableThatStakedIt(t *testing.T) {
	// The wager phase is in this list because that is where the amount is
	// typed: "Locked in — $7,400" has to render on the phone that typed it
	// while the other nineteen see only a lit pip.
	for _, phase := range []Phase{PhaseWager, PhaseQuestion, PhaseBetting} {
		snap, teamA, stake := finalWithStake(phase)

		own := ProjectPlayer(snap, teamA)
		if own.You == nil || own.You.Stake == nil || *own.You.Stake != stake {
			t.Fatalf("%s: the staking table cannot see its own wager: %+v", phase, own.You)
		}

		// Everywhere else the amount must be ABSENT, not zeroed. Marshalling
		// and searching the bytes is the only check that still catches this
		// when somebody adds a field to a public struct later.
		otherID := snap.Teams[1].ID
		elsewhere := map[string]any{
			"the TV":            ProjectDisplay(snap),
			"another phone":     ProjectPlayer(snap, otherID),
			"a spectator":       ProjectPlayer(snap, uuid.Nil),
			"its own team rows": own.Teams,
		}
		for label, frame := range elsewhere {
			raw, err := json.Marshal(frame)
			if err != nil {
				t.Fatalf("%s: marshalling %s: %v", phase, label, err)
			}
			if strings.Contains(string(raw), "7400") {
				t.Fatalf("%s: %s carries the wager %d:\n%s", phase, label, stake, raw)
			}
		}

		// A table that has not staked gets nil, not a zero it would render as
		// "you bet nothing".
		if other := ProjectPlayer(snap, otherID); other.You == nil || other.You.Stake != nil {
			t.Fatalf("%s: a table with no wager reports %+v", phase, other.You)
		}
	}
}

// A spectator — somebody who opened the URL with no cookie — gets the full
// read-only view and nothing private. The stream must work with no cookie,
// not 401.
func TestSpectatorFrameCarriesNoPrivateState(t *testing.T) {
	snap, _ := snapshotIn(PhaseBetting, false)
	frame := ProjectPlayer(snap, uuid.Nil)
	if frame.You != nil {
		t.Fatalf("spectator frame carries a `you` block: %+v", frame.You)
	}
	if len(frame.Teams) != 2 || len(frame.Slots) != 2 {
		t.Fatal("spectator frame is missing the public view it should have")
	}
}

// A phone sees its OWN chips and its own delta, and nobody else's private
// state.
func TestPlayerFrameCarriesOwnChipsOnly(t *testing.T) {
	snap, teamA := snapshotIn(PhaseBetting, false)
	frame := ProjectPlayer(snap, teamA)
	if frame.You == nil || len(frame.You.Chips) != 1 || frame.You.Chips[0].Amount != 200 {
		t.Fatalf("own chips = %+v", frame.You)
	}
	other := snap.Teams[1].ID
	otherFrame := ProjectPlayer(snap, other)
	if otherFrame.You == nil || len(otherFrame.You.Chips) != 0 {
		t.Fatalf("another team's frame shows %+v", otherFrame.You)
	}
}

// TestBetsLandLiveFromBettingOnward. The room watches the chips arrive: a
// card with nothing on it for forty-five seconds reads as a frozen screen,
// and the chip that moves with ten seconds left is the best thing on the
// wall. What is still withheld is anything from BEFORE betting opens --
// during the reveal the cards carry no chips and no pot, because nothing has
// been placed and a stale pot would be a lie.
func TestBetsLandLiveFromBettingOnward(t *testing.T) {
	snap, teamID := snapshotIn(PhaseBetting, false)

	display := ProjectDisplay(snap)
	shownOnTV, potOnTV := 0, 0
	for _, sl := range display.Slots {
		shownOnTV += len(sl.Chips)
		potOnTV += sl.Pot
	}
	if shownOnTV == 0 {
		t.Fatal("the TV shows no chips during betting — the room cannot watch them land")
	}
	if potOnTV == 0 {
		t.Fatal("the TV shows no pot during betting")
	}
	// The chips say WHOSE they are — a bare stack of money tells the room
	// nothing about who is chasing what.
	if display.Slots[0].Chips[0].Team == "" {
		t.Fatalf("a chip on the TV carries no team name: %+v", display.Slots[0].Chips[0])
	}

	// Another table's chip reaches a phone too, so the small screen and the
	// big one agree about what is on the table.
	other := snap.Teams[1].ID
	player := ProjectPlayer(snap, other)
	chipsOnPhone, potOnPhone := 0, 0
	for _, sl := range player.Slots {
		chipsOnPhone += len(sl.Chips)
		potOnPhone += sl.Pot
	}
	if chipsOnPhone == 0 {
		t.Fatal("a phone cannot see another table's chips during betting")
	}
	if potOnPhone == 0 {
		t.Fatal("a phone cannot see the pot during betting")
	}

	// A table always sees its OWN chips, in the private shape it needs to
	// draw its own tokens as placed.
	own := ProjectPlayer(snap, teamID)
	if own.You == nil || len(own.You.Chips) != 1 {
		t.Fatalf("a table cannot see its own chips: %+v", own.You)
	}

	// Before betting opens there is nothing to show, and the cards must not
	// carry a pot from a previous life.
	reveal, _ := snapshotIn(PhaseReveal, false)
	for _, sl := range ProjectDisplay(reveal).Slots {
		if len(sl.Chips) != 0 {
			t.Fatalf("the TV shows %d chips during the reveal: %+v", len(sl.Chips), sl.Chips)
		}
		if sl.Pot != 0 {
			t.Fatalf("the TV shows a pot of %d during the reveal", sl.Pot)
		}
	}
	for _, sl := range ProjectPlayer(reveal, other).Slots {
		if len(sl.Chips) != 0 || sl.Pot != 0 {
			t.Fatalf("a phone sees bets during the reveal: %+v", sl)
		}
	}

	// The host sees everything throughout: they need to know who has not
	// placed.
	host := ProjectHost(snap)
	total := 0
	for _, sl := range host.Slots {
		total += len(sl.Chips)
	}
	if total == 0 {
		t.Fatal("the host cannot see the chips during betting")
	}

	// And once scored the chips are still there to be paid or swept.
	scored, _ := snapshotIn(PhaseScoring, true)
	shown := 0
	for _, sl := range ProjectDisplay(scored).Slots {
		shown += len(sl.Chips)
	}
	if shown == 0 {
		t.Fatal("the chips vanish at scoring")
	}
}

// The rules come from one place, so the wall and the phone cannot tell a room
// different games — and a night with the final switched off is not told about
// a mechanic it does not have.
func TestRulesAreServedAndMatchTheGame(t *testing.T) {
	snap, teamID := snapshotIn(PhaseLobby, false)

	withFinal := ProjectPlayer(snap, teamID).Rules
	if len(withFinal) != 6 {
		t.Fatalf("got %d rules with the final on, want 6", len(withFinal))
	}
	if !strings.Contains(withFinal[len(withFinal)-1], "Last question") {
		t.Fatalf("the last rule is not the final wager: %q", withFinal[len(withFinal)-1])
	}

	snap.FinalWager = false
	withoutFinal := ProjectPlayer(snap, teamID).Rules
	if len(withoutFinal) != 5 {
		t.Fatalf("got %d rules with the final off, want 5", len(withoutFinal))
	}
	for _, r := range withoutFinal {
		if strings.Contains(r, "Last question") {
			t.Fatal("a game with no final wager is told about the final wager")
		}
	}

	// A spectator sees them too — deciding whether to join is exactly when
	// somebody needs to know what the game is.
	if len(ProjectPlayer(snap, uuid.Nil).Rules) != 5 {
		t.Fatal("a spectator cannot see the rules")
	}
}

// A latecomer's own frame has to say WHICH question it is in from, because
// "sitting this one out" with no number reads like a punishment rather than a
// two-minute wait. A table that has been here all along is told nothing —
// there is nothing to tell it.
func TestPrivateFrameTellsALatecomerWhichQuestionItIsIn(t *testing.T) {
	snap, early := snapshotIn(PhaseQuestion, false)
	late := snap.Teams[1].ID
	snap.Teams[1].Eligible = false
	snap.Teams[1].EligibleFrom = snap.Round.Ordinal + 1
	snap.Teams[0].EligibleFrom = 1

	lateYou := ProjectPlayer(snap, late).You
	if lateYou == nil {
		t.Fatal("the latecomer has no private block at all")
	}
	if lateYou.Eligible {
		t.Fatal("a table that joined mid-question is told it is eligible")
	}
	if lateYou.InFromQuestion != 4 {
		t.Fatalf("inFromQuestion = %d, want 4 — the question after the one in play", lateYou.InFromQuestion)
	}

	earlyYou := ProjectPlayer(snap, early).You
	if !earlyYou.Eligible {
		t.Fatal("a table that was here all along is told it is sitting out")
	}
	if earlyYou.InFromQuestion != 0 {
		t.Fatalf("inFromQuestion = %d for a table already in; it should be omitted", earlyYou.InFromQuestion)
	}
	// omitempty, so the field is not on the wire at all for a table already in.
	raw, err := json.Marshal(ProjectPlayer(snap, early))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "inFromQuestion") {
		t.Fatalf("an eligible table's frame carries inFromQuestion:\n%s", raw)
	}

	// The public list says only whether a table is in, never from when: the
	// rest of the room does not need the other tables' arrival times.
	for _, wt := range ProjectDisplay(snap).Teams {
		if wt.ID == late.String() && wt.Eligible {
			t.Fatal("the TV thinks the latecomer is in this round")
		}
	}
}
