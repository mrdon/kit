package trivia

// Board rounds: which board is in play, and what it is worth.
//
// A round is NOT a new ruleset. The room is told the same five lines and
// plays them twice with a break in between -- see Rules(), which is the
// forcing function for scope. What a round changes is the schedule and the
// stakes, and both are arithmetic.

// boardMultiplier is what a board round is worth against the first one.
//
// Doubling, and it applies to the CELLS AND THE CHIPS TOGETHER. That pairing
// is the whole point: only the table that wrote the winning answer takes a
// cell, but every table bets every round, so the betting is meant to be the
// bigger channel. Double the cells alone and knowing starts to outrun reading
// the room; double the chips alone and the board stops mattering. Doubling
// both leaves the balance exactly where round one set it and simply raises
// the stakes -- which is Double Jeopardy's actual job, comeback potential. A
// table that had a bad first half is still live after the break.
//
// There is deliberately no setting for this. A per-round value knob would be
// a rule the host has to explain to the room; a flat "round two is worth
// double" is one sentence and nobody has to be told it twice.
func boardMultiplier(round int) int {
	if round <= 0 {
		return 1
	}
	return 1 << round
}

// scaleValues applies a round's multiplier to a list of chip values.
func scaleValues(values []int, round int) []int {
	m := boardMultiplier(round)
	if m == 1 {
		return values
	}
	out := make([]int, len(values))
	for i, v := range values {
		out[i] = v * m
	}
	return out
}

// CurrentBoardRound is which board the game is on, derived from the cells
// rather than stored.
//
// DERIVED ON PURPOSE. A stored "current round" is a second source of truth
// that can disagree with the board, and the board is the one the room is
// looking at. The lowest round index that still has an unplayed cell IS the
// round in play, by definition, and it needs no migration, cannot drift, and
// is correct again the moment a cell is played.
//
// An exhausted board returns the count of rounds, which is what afterScoring
// tests against to know the night's boards are done. Callers that need "which
// round's SCALING applies" want PlayingBoardRound instead.
func CurrentBoardRound(cells []BoardCell) int {
	best := -1
	rounds := 0
	for _, c := range cells {
		if c.RoundIndex+1 > rounds {
			rounds = c.RoundIndex + 1
		}
		if c.PlayedAt == nil && (best < 0 || c.RoundIndex < best) {
			best = c.RoundIndex
		}
	}
	if best < 0 {
		return rounds
	}
	return best
}

// CellsInRound narrows a board to one round, which is what every surface
// shows: the room sees the board it is playing, never the one after it.
func CellsInRound(cells []BoardCell, round int) []BoardCell {
	out := make([]BoardCell, 0, len(cells))
	for _, c := range cells {
		if c.RoundIndex == round {
			out = append(out, c)
		}
	}
	return out
}

// BoardRoundCount is how many rounds a built board actually holds, which can
// lag the game's setting if the board was built before it changed.
func BoardRoundCount(cells []BoardCell) int {
	rounds := 0
	for _, c := range cells {
		if c.RoundIndex+1 > rounds {
			rounds = c.RoundIndex + 1
		}
	}
	return rounds
}

// PlayingBoardRound is CurrentBoardRound clamped to a real round.
//
// The difference matters exactly once: when every cell has been played,
// CurrentBoardRound reports the round COUNT -- one past the end -- which is
// what tells afterScoring the boards are done. But the final still has to be
// scaled like something, and the last round is the honest answer.
//
// This is one function rather than the four lines it replaces because those
// four lines decided two things that must agree by construction: what a chip
// is WORTH when it lands (chipAmount) and what the phone was SHOWN before it
// was placed (the snapshot). They agreed by copy-paste, which is the same
// thing right up until one of them is edited.
func PlayingBoardRound(cells []BoardCell) int {
	round := CurrentBoardRound(cells)
	if n := BoardRoundCount(cells); n > 0 && round >= n {
		return n - 1
	}
	return round
}
