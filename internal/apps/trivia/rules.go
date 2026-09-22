package trivia

import "strings"

// The rules, as the host reads them out.
//
// This is the ONE place they are written. The TV renders them into the lobby
// screen and the phone shows them when a table joins, both from this list, so
// the two can never tell a room different games.
//
// It is also the forcing function for scope: if the game cannot be explained
// in these lines, the game is too complicated. Nothing here is a
// simplification of a longer rulebook — there is no longer rulebook.
//
// finalWager drops the last line when the game is not playing one, so a night
// with it switched off is not told about a mechanic it does not have.
//
// BOARD ROUNDS ADD NOTHING HERE, and that is the test they had to pass. A
// two-round night is the same five lines played twice with a break in the
// middle; the doubling is a number on the wall, not a rule anybody has to be
// told. If a future round type cannot be added without a sixth line, it is
// the round type that is wrong.
// tokens are the chip values AS THIS ROUND HAS THEM, already scaled. They are
// a parameter rather than the shipped $100/$200 written into the sentence
// because a later round doubles or trebles them, and the break is the best
// entry of the night: the most likely new player of the evening reads these
// lines and is then handed chips that do not match them.
func Rules(finalWager bool, tokens []int) []string {
	rules := []string{
		"Everybody types a number. Closest without going over wins.",
		"If everyone's too high, “smaller than all of these” wins.",
		"Whoever wrote the winning answer takes the board money.",
		"Then everyone bets. Put " + chipSentence(tokens) + " on one answer, or split them across two.",
		"Chips on the winning answer pay their value. Wrong chips cost you nothing.",
	}
	if finalWager {
		rules = append(rules,
			"Last question. You see the category first, and set your wager before you see the question. "+
				"Then put it on whichever answer you like. Right doubles it, wrong loses it.")
	}
	return rules
}

// chipSentence names a table's chips the way a host would read them out.
func chipSentence(tokens []int) string {
	if len(tokens) == 0 {
		return "your chips"
	}
	parts := make([]string, 0, len(tokens))
	for _, t := range tokens {
		parts = append(parts, FormatMoney(t))
	}
	if len(parts) == 1 {
		return "your " + parts[0] + " chip"
	}
	return "your " + strings.Join(parts[:len(parts)-1], ", ") +
		" and " + parts[len(parts)-1] + " chips"
}
