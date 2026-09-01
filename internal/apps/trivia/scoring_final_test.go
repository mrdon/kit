package trivia

import (
	"testing"

	"github.com/google/uuid"
)

// The final is the one branch where a score can go DOWN, so it gets its own
// file and its cases are written against the rules as the host reads them
// out: right doubles your wager, wrong loses it, $0 is a legal bet, and
// nobody finishes below zero.

func finalInput(slots []Slot, correct float64, bets []RoundBet, banks map[uuid.UUID]int) RoundInput {
	return RoundInput{
		Correct: correct, CellPoints: 1000, Slots: slots,
		Bets: bets, IsFinal: true, Banks: banks,
	}
}

// A winning wager pays its amount, which on top of the stake the team still
// holds is the "doubles it" the rules promise.
func TestFinalWinningWagerDoublesTheStake(t *testing.T) {
	ids := teamIDs(2)
	slots := slotsFrom(
		TeamAnswer{TeamID: ids[0], Value: 40},
		TeamAnswer{TeamID: ids[1], Value: 90},
	)
	win := posOf(t, slots, 40)
	res := ScoreRound(finalInput(slots, 50,
		[]RoundBet{{TeamID: ids[1], Amount: 3000, SlotPos: win}},
		map[uuid.UUID]int{ids[1]: 4000}))

	if got := res.Deltas[ids[1]].BetDelta; got != 3000 {
		t.Fatalf("winning wager delta = %d, want +3000 (4000 -> 7000)", got)
	}
}

// Wrong loses the WHOLE wager, not half. Lose-half is gentler but removes the
// leader's dilemma entirely: at half risk the leader simply bets big too,
// ratios are preserved, and the mechanism stops working.
func TestFinalLosingWagerCostsAllOfIt(t *testing.T) {
	ids := teamIDs(2)
	slots := slotsFrom(
		TeamAnswer{TeamID: ids[0], Value: 40},
		TeamAnswer{TeamID: ids[1], Value: 90},
	)
	lose := posOf(t, slots, 90)
	res := ScoreRound(finalInput(slots, 50,
		[]RoundBet{{TeamID: ids[0], Amount: 2000, SlotPos: lose}},
		map[uuid.UUID]int{ids[0]: 5000}))

	if got := res.Deltas[ids[0]].BetDelta; got != -2000 {
		t.Fatalf("losing wager delta = %d, want -2000", got)
	}
}

// $0 is a first-class choice, not a fallback -- it is the leader's defensive
// play and it has to be a genuine no-op.
func TestFinalZeroWagerIsLegalAndCostsNothing(t *testing.T) {
	ids := teamIDs(2)
	slots := slotsFrom(
		TeamAnswer{TeamID: ids[0], Value: 40},
		TeamAnswer{TeamID: ids[1], Value: 90},
	)
	lose := posOf(t, slots, 90)
	res := ScoreRound(finalInput(slots, 50,
		[]RoundBet{{TeamID: ids[1], Amount: 0, SlotPos: lose}},
		map[uuid.UUID]int{ids[1]: 8000}))

	if got := res.Deltas[ids[1]].BetDelta; got != 0 {
		t.Fatalf("zero wager moved the score by %d", got)
	}
}

// The floor. A team can reach exactly $0 -- an all-in that misses -- and
// never a cent below it. "Finished on minus four hundred dollars" is the kind
// of thing a room notices.
func TestFinalCanReachZeroButNeverGoNegative(t *testing.T) {
	ids := teamIDs(2)
	slots := slotsFrom(
		TeamAnswer{TeamID: ids[0], Value: 40},
		TeamAnswer{TeamID: ids[1], Value: 90},
	)
	lose := posOf(t, slots, 90)

	// All-in and wrong: exactly zero.
	res := ScoreRound(finalInput(slots, 50,
		[]RoundBet{{TeamID: ids[1], Amount: 2000, SlotPos: lose}},
		map[uuid.UUID]int{ids[1]: 2000}))
	if final := 2000 + res.Deltas[ids[1]].Total(); final != 0 {
		t.Fatalf("all-in loss finished at %d, want exactly 0", final)
	}

	// A stake somehow larger than the bank -- a hand-edited request that got
	// past the clamp -- still cannot go below zero.
	res = ScoreRound(finalInput(slots, 50,
		[]RoundBet{{TeamID: ids[1], Amount: 9999, SlotPos: lose}},
		map[uuid.UUID]int{ids[1]: 2000}))
	if final := 2000 + res.Deltas[ids[1]].Total(); final != 0 {
		t.Fatalf("oversized loss finished at %d, want 0 — never negative", final)
	}
}

// A team that locked a stake but never typed a number still has its bet
// scored: the wager is on somebody's answer, not its own.
func TestFinalStakedButNeverAnsweredStillScoresItsBet(t *testing.T) {
	ids := teamIDs(2)
	slots := slotsFrom(TeamAnswer{TeamID: ids[0], Value: 40})
	win := posOf(t, slots, 40)
	res := ScoreRound(finalInput(slots, 50,
		[]RoundBet{{TeamID: ids[1], Amount: 1500, SlotPos: win}},
		map[uuid.UUID]int{ids[1]: 1500}))

	d := res.Deltas[ids[1]]
	if d.BoardPoints != 0 || d.BetDelta != 1500 {
		t.Fatalf("got %+v, want board 0 and bet +1500", d)
	}
}

// Board points work exactly as in any other round: writing the winning answer
// pays the round's value, on top of whatever the wager did.
func TestFinalBoardPointsLandNormally(t *testing.T) {
	ids := teamIDs(2)
	slots := slotsFrom(
		TeamAnswer{TeamID: ids[0], Value: 40},
		TeamAnswer{TeamID: ids[1], Value: 90},
	)
	win := posOf(t, slots, 40)
	res := ScoreRound(finalInput(slots, 50,
		[]RoundBet{{TeamID: ids[0], Amount: 500, SlotPos: win}},
		map[uuid.UUID]int{ids[0]: 3000}))

	d := res.Deltas[ids[0]]
	if d.BoardPoints != 1000 {
		t.Fatalf("board points = %d, want 1000", d.BoardPoints)
	}
	if d.BetDelta != 500 {
		t.Fatalf("bet delta = %d, want +500", d.BetDelta)
	}
}

// The staking rule that makes the final a judgement rather than a lottery:
// you back somebody's answer, and identical guesses collapse, so several
// tables can share the winning card and all be paid.
func TestFinalSeveralTeamsCanBackTheSameWinningCard(t *testing.T) {
	ids := teamIDs(3)
	slots := slotsFrom(
		TeamAnswer{TeamID: ids[0], Value: 40},
		TeamAnswer{TeamID: ids[1], Value: 40},
		TeamAnswer{TeamID: ids[2], Value: 90},
	)
	win := posOf(t, slots, 40)
	res := ScoreRound(finalInput(slots, 50, []RoundBet{
		{TeamID: ids[0], Amount: 100, SlotPos: win},
		{TeamID: ids[1], Amount: 200, SlotPos: win},
		{TeamID: ids[2], Amount: 300, SlotPos: win},
	}, map[uuid.UUID]int{ids[0]: 100, ids[1]: 200, ids[2]: 300}))

	for i, want := range []int{100, 200, 300} {
		if got := res.Deltas[ids[i]].BetDelta; got != want {
			t.Fatalf("team %d bet delta = %d, want %d", i, got, want)
		}
	}
	// And both writers of 40 take the round's board points.
	for i := range 2 {
		if got := res.Deltas[ids[i]].BoardPoints; got != 1000 {
			t.Fatalf("team %d board points = %d, want 1000", i, got)
		}
	}
}

// With the final switched off nothing here is reachable, but the engine still
// has to behave: IsFinal false means a wrong chip is free, full stop.
func TestFinalFlagOffMeansWrongChipsAreStillFree(t *testing.T) {
	ids := teamIDs(2)
	slots := slotsFrom(
		TeamAnswer{TeamID: ids[0], Value: 40},
		TeamAnswer{TeamID: ids[1], Value: 90},
	)
	lose := posOf(t, slots, 90)
	res := ScoreRound(RoundInput{
		Correct: 50, CellPoints: 1000, Slots: slots, IsFinal: false,
		Bets:  []RoundBet{{TeamID: ids[0], Amount: 2000, SlotPos: lose}},
		Banks: map[uuid.UUID]int{ids[0]: 5000},
	})
	if got := res.Deltas[ids[0]].BetDelta; got != 0 {
		t.Fatalf("bet delta = %d outside a final, want 0", got)
	}
}
