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
			CorrectValue: 867530.0, CorrectText: "867530",
			AnsweredCount: 1, EligibleCount: 2,
		},
		Slots: []SnapSlot{
			{ID: slotA, Position: 1, Value: &v2, Label: "271828", TeamIDs: []uuid.UUID{teamA}, TeamNames: []string{"Bar Flies"}},
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
	preScoring := []Phase{PhaseSetup, PhaseLobby, PhaseBoard, PhaseQuestion, PhaseReveal, PhaseBetting}

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

// The final's tension is that nobody knows whether the leader defended or sat
// out. The TV shows LOCKED, never the amount, until scoring.
func TestPublicFramesShowStakeLockedWithoutTheAmount(t *testing.T) {
	snap, _ := snapshotIn(PhaseQuestion, false)
	snap.Round.IsFinal = true
	stake := 7400
	snap.Teams[0].StakeLocked = true

	frame := ProjectDisplay(snap)
	if !frame.Teams[0].StakeLocked {
		t.Fatal("TV frame does not show the stake as locked")
	}
	raw, _ := json.Marshal(frame)
	if strings.Contains(string(raw), "7400") {
		t.Fatalf("the stake amount %d appears in the TV frame:\n%s", stake, raw)
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
