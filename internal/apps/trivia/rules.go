package trivia

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
func Rules(finalWager bool) []string {
	rules := []string{
		"Everybody types a number. Closest without going over wins.",
		"If everyone's too high, “smaller than all of these” wins.",
		"Whoever wrote the winning answer takes the board money.",
		"Then everyone bets: your $100 chip and your $200 chip, on two different answers.",
		"Chips on the winning answer pay their value. Wrong chips cost you nothing.",
	}
	if finalWager {
		rules = append(rules,
			"Last question: set your bet when you answer, before you see anything. "+
				"Then put it on whichever answer you like. Right doubles it, wrong loses it.")
	}
	return rules
}
