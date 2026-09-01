package trivia

import (
	"testing"

	"github.com/google/uuid"
)

// teamIDs makes n stable ids for a test.
func teamIDs(n int) []uuid.UUID {
	out := make([]uuid.UUID, n)
	for i := range out {
		out[i] = uuid.New()
	}
	return out
}

func slotsFrom(answers ...TeamAnswer) []Slot { return BuildSlots(answers) }

// posOf finds the slot a given value landed in, so the tests can express bets
// as "the card saying 1969" rather than an index they would have to keep in
// step with BuildSlots.
func posOf(t *testing.T, slots []Slot, value float64) int {
	t.Helper()
	for _, s := range slots {
		if s.Value != nil && *s.Value == value {
			return s.Position
		}
	}
	t.Fatalf("no slot with value %v", value)
	return -1
}

// The ordinary case: somebody is exactly right.
func TestScoreExactHitTakesTheCell(t *testing.T) {
	ids := teamIDs(2)
	slots := slotsFrom(
		TeamAnswer{TeamID: ids[0], Value: 1969},
		TeamAnswer{TeamID: ids[1], Value: 1971},
	)
	res := ScoreRound(RoundInput{Correct: 1969, CellPoints: 500, Slots: slots})

	if got := res.Deltas[ids[0]].BoardPoints; got != 500 {
		t.Fatalf("exact answer took %d board points, want 500", got)
	}
	if got := res.Deltas[ids[1]].BoardPoints; got != 0 {
		t.Fatalf("overshooting team took %d board points, want 0", got)
	}
}

// Rule 1 proper: nobody was exact, so the largest guess at or below wins --
// and the closer-but-over guess takes nothing.
func TestScoreClosestWithoutGoingOver(t *testing.T) {
	ids := teamIDs(3)
	slots := slotsFrom(
		TeamAnswer{TeamID: ids[0], Value: 100},
		TeamAnswer{TeamID: ids[1], Value: 140}, // closest in absolute terms
		TeamAnswer{TeamID: ids[2], Value: 130}, // but this one is under
	)
	res := ScoreRound(RoundInput{Correct: 135, CellPoints: 1000, Slots: slots})

	if got := res.Deltas[ids[2]].BoardPoints; got != 1000 {
		t.Fatalf("closest-under team took %d, want 1000", got)
	}
	if got := res.Deltas[ids[1]].BoardPoints; got != 0 {
		t.Fatalf("nearest-but-over team took %d, want 0 — going over loses", got)
	}
}

// Rule 2, and the case that justifies the pseudo-slot existing at all: every
// guess overshot. The card wins, and because nobody wrote it, NOBODY takes
// the board points.
func TestScoreEveryGuessTooHighPaysNoBoardPoints(t *testing.T) {
	ids := teamIDs(2)
	slots := slotsFrom(
		TeamAnswer{TeamID: ids[0], Value: 50},
		TeamAnswer{TeamID: ids[1], Value: 80},
	)
	res := ScoreRound(RoundInput{Correct: 12, CellPoints: 500, Slots: slots})

	if res.WinningPos != 0 {
		t.Fatalf("winning position = %d, want 0 (the pseudo-slot)", res.WinningPos)
	}
	for i, id := range ids {
		if got := res.Deltas[id].BoardPoints; got != 0 {
			t.Fatalf("team %d took %d board points when nobody wrote the winner", i, got)
		}
	}
}

// A bet on the pseudo-slot pays like any other. Backing "it is lower than
// anything up there" is a real read of the room, not a consolation.
func TestScoreBetOnPseudoSlotPaysNormally(t *testing.T) {
	ids := teamIDs(2)
	slots := slotsFrom(
		TeamAnswer{TeamID: ids[0], Value: 50},
		TeamAnswer{TeamID: ids[1], Value: 80},
	)
	res := ScoreRound(RoundInput{
		Correct: 12, CellPoints: 500, Slots: slots,
		Bets: []RoundBet{{TeamID: ids[1], Amount: 200, SlotPos: 0}},
	})
	if got := res.Deltas[ids[1]].BetDelta; got != 200 {
		t.Fatalf("chip on the pseudo-slot paid %d, want 200", got)
	}
}

// The dedupe's payoff, and why tie-breaking never had to be designed: two
// tables that wrote the same number share one card and BOTH take full cell
// value. Splitting it would punish agreement.
func TestScoreTiedAnswersBothTakeFullValue(t *testing.T) {
	ids := teamIDs(3)
	slots := slotsFrom(
		TeamAnswer{TeamID: ids[0], Value: 1969},
		TeamAnswer{TeamID: ids[1], Value: 1969},
		TeamAnswer{TeamID: ids[2], Value: 2000},
	)
	res := ScoreRound(RoundInput{Correct: 1980, CellPoints: 1000, Slots: slots})

	for i := range 2 {
		if got := res.Deltas[ids[i]].BoardPoints; got != 1000 {
			t.Fatalf("team %d took %d, want the full 1000 — agreement is not punished", i, got)
		}
	}
}

// Rule 5: during the board, a wrong chip costs nothing. This is the property
// that makes every board question risk-free.
func TestScoreWrongChipsCostNothingOnTheBoard(t *testing.T) {
	ids := teamIDs(2)
	slots := slotsFrom(
		TeamAnswer{TeamID: ids[0], Value: 10},
		TeamAnswer{TeamID: ids[1], Value: 20},
	)
	loser := posOf(t, slots, 20)
	res := ScoreRound(RoundInput{
		Correct: 15, CellPoints: 500, Slots: slots,
		Bets: []RoundBet{{TeamID: ids[1], Amount: 200, SlotPos: loser}},
	})
	if got := res.Deltas[ids[1]].BetDelta; got != 0 {
		t.Fatalf("wrong chip moved the score by %d, want 0", got)
	}
}

// Backing your own answer is allowed and pays like anything else.
func TestScoreBettingOnYourOwnAnswer(t *testing.T) {
	ids := teamIDs(2)
	slots := slotsFrom(
		TeamAnswer{TeamID: ids[0], Value: 10},
		TeamAnswer{TeamID: ids[1], Value: 20},
	)
	win := posOf(t, slots, 10)
	res := ScoreRound(RoundInput{
		Correct: 12, CellPoints: 500, Slots: slots,
		Bets: []RoundBet{{TeamID: ids[0], Amount: 100, SlotPos: win}},
	})
	d := res.Deltas[ids[0]]
	if d.BoardPoints != 500 || d.BetDelta != 100 {
		t.Fatalf("own-answer bet gave %+v, want board 500 and bet 100", d)
	}
}

// A team that never typed a number can still bet -- it is at the bar, not out
// of the game.
func TestScoreTeamThatBetButNeverAnswered(t *testing.T) {
	ids := teamIDs(2)
	slots := slotsFrom(TeamAnswer{TeamID: ids[0], Value: 10})
	win := posOf(t, slots, 10)
	res := ScoreRound(RoundInput{
		Correct: 10, CellPoints: 500, Slots: slots,
		Bets: []RoundBet{{TeamID: ids[1], Amount: 200, SlotPos: win}},
	})
	d := res.Deltas[ids[1]]
	if d.BoardPoints != 0 || d.BetDelta != 200 {
		t.Fatalf("silent bettor got %+v, want board 0 and bet 200", d)
	}
}

// And a team that bet nothing loses nothing.
func TestScoreZeroBetsIsFine(t *testing.T) {
	ids := teamIDs(1)
	slots := slotsFrom(TeamAnswer{TeamID: ids[0], Value: 10})
	res := ScoreRound(RoundInput{Correct: 10, CellPoints: 500, Slots: slots})
	if got := res.Deltas[ids[0]].BetDelta; got != 0 {
		t.Fatalf("bet delta = %d with no bets placed, want 0", got)
	}
}

// The forced spread caps betting income at ONE chip: two chips on two
// different answers, only one answer wins, so $200 a round is the ceiling --
// not $300. Sizing the economy against $300 is an easy mistake to make and
// this is the guard against it.
func TestScoreBettingIncomeIsCappedAtOneChip(t *testing.T) {
	ids := teamIDs(3)
	slots := slotsFrom(
		TeamAnswer{TeamID: ids[0], Value: 10},
		TeamAnswer{TeamID: ids[1], Value: 20},
		TeamAnswer{TeamID: ids[2], Value: 30},
	)
	res := ScoreRound(RoundInput{
		Correct: 25, CellPoints: 500, Slots: slots,
		Bets: []RoundBet{
			{TeamID: ids[0], Amount: 200, SlotPos: posOf(t, slots, 20), TokenIdx: 1},
			{TeamID: ids[0], Amount: 100, SlotPos: posOf(t, slots, 10), TokenIdx: 0},
		},
	})
	if got := res.Deltas[ids[0]].BetDelta; got != 200 {
		t.Fatalf("betting income = %d, want 200 — only one of two chips can win", got)
	}
}
