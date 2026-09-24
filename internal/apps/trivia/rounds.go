package trivia

// Board rounds: which board is in play, and what it is worth.
//
// A round is NOT a new ruleset. The room is told the same five lines and
// plays them twice with a break in between -- see Rules(), which is the
// forcing function for scope. What a round changes is the schedule and the
// stakes, and both are arithmetic.

// boardMultiplier is what a board round is worth against the first one.
//
// LINEAR -- round one at 1x, round two at 2x, round three at 3x -- and it
// applies to THE CHIPS. A cell is worth what it was worth all night; what a
// later round raises is what you can put on somebody else's answer. Writing
// the winner is one table's moment, and it stays worth the same one; the
// stakes the whole room plays for are the ones that climb.
//
// It used to double (1, 2, 4, 8), which is what Jeopardy does and is fine at
// two rounds. It compounds badly past that: at four rounds the last board is
// worth 53% of the night and everything before it is noise -- a table can
// play badly for two hours and win on the last board, which is not comeback
// potential, it is erasure, and the tables that played well will say so.
// Linear keeps a round-one sweep worth a quarter of a round-four one instead
// of an eighth, so the early boards still decide something in a long night.
//
// There is deliberately no setting for this. A per-round value knob would be
// a rule the host has to explain to the room; "each round is worth one more
// than the last" is a sentence nobody has to be told twice.
func boardMultiplier(round int) int {
	if round < 0 {
		return 1
	}
	return round + 1
}

// scaleValues applies a round's multiplier to a list of chip values.
//
// Chips only. Cell values are stored and paid unscaled, and passing them
// through here would have the wall promising a number scoring does not pay.
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
// ScaleRoundOf is the board round whose chip values a round is played at.
//
// It reads the round's OWN CELL rather than the state of the board, because
// played_at is stamped when a cell is OPENED, not when its round is scored.
// So the moment the host opens the last cell of a round, that round has no
// unplayed cells left and PlayingBoardRound has already moved on -- while the
// question is still on the wall and the room is still betting on it. Every
// board round's last question was therefore priced at the NEXT round's
// chips: $200/$400 in round one.
//
// A final has no cell and takes its stake from the wager instead, so the
// fallback here is only ever reached between rounds, where "the round about
// to start" is exactly what the break screen means.
func ScaleRoundOf(round *Round, cells []BoardCell) int {
	if round != nil && round.CellID != nil {
		for _, c := range cells {
			if c.ID == *round.CellID {
				return c.RoundIndex
			}
		}
	}
	return PlayingBoardRound(cells)
}

func PlayingBoardRound(cells []BoardCell) int {
	round := CurrentBoardRound(cells)
	if n := BoardRoundCount(cells); n > 0 && round >= n {
		return n - 1
	}
	return round
}
