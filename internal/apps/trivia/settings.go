package trivia

import "fmt"

// Bounds on the per-game knobs. These are not a schema -- the database
// carries defaults -- they are what stops a host from building something the
// TV cannot render or the room cannot finish.
const (
	maxBoardColumns = 8
	maxBoardRows    = 5
	minPhaseSeconds = 5
	maxPhaseSeconds = 600
	maxTokens       = 4
)

// DefaultSettings is the shipped game: 5 categories x 2 rows at $500/$1000,
// ten questions, two chips at $100/$200, and a final.
//
// Deliberately not Jeopardy's 30-clue shape. Jeopardy works at that size
// because one person buzzes and answers in seconds; here EVERY team types an
// answer, watches a reveal and places bets, so a question costs about three
// minutes end to end. Ten questions is roughly half an hour of board plus a
// lobby and a final -- a bar game people finish.
func DefaultSettings() Settings {
	return Settings{
		BoardRows: 2, BoardColumns: 5,
		CellValues: []int{500, 1000}, TokenValues: []int{100, 200},
		FinalWager: true, AnswerSeconds: 60, RevealSeconds: 15, BetSeconds: 45,
	}
}

// normaliseSettings fills in anything the client left at zero, so a console
// that only wants to flip final_wager does not have to resend the world.
func normaliseSettings(s Settings) Settings {
	d := DefaultSettings()
	if s.BoardRows == 0 {
		s.BoardRows = d.BoardRows
	}
	if s.BoardColumns == 0 {
		s.BoardColumns = d.BoardColumns
	}
	if len(s.CellValues) == 0 {
		s.CellValues = d.CellValues
	}
	if len(s.TokenValues) == 0 {
		s.TokenValues = d.TokenValues
	}
	if s.AnswerSeconds == 0 {
		s.AnswerSeconds = d.AnswerSeconds
	}
	if s.RevealSeconds == 0 {
		s.RevealSeconds = d.RevealSeconds
	}
	if s.BetSeconds == 0 {
		s.BetSeconds = d.BetSeconds
	}
	return s
}

// validateSettings rejects a configuration that could not be played.
func validateSettings(s Settings) error {
	if s.BoardColumns < 1 || s.BoardColumns > maxBoardColumns {
		return fmt.Errorf("a board has 1 to %d columns", maxBoardColumns)
	}
	if s.BoardRows < 1 || s.BoardRows > maxBoardRows {
		return fmt.Errorf("a board has 1 to %d rows", maxBoardRows)
	}
	if len(s.CellValues) != s.BoardRows {
		return fmt.Errorf("a board with %d rows needs %d cell values, got %d",
			s.BoardRows, s.BoardRows, len(s.CellValues))
	}
	for _, v := range s.CellValues {
		if v <= 0 {
			return fmt.Errorf("cell values must be positive, got %d", v)
		}
	}
	if len(s.TokenValues) < 1 || len(s.TokenValues) > maxTokens {
		return fmt.Errorf("a team carries 1 to %d chips", maxTokens)
	}
	for _, v := range s.TokenValues {
		if v <= 0 {
			return fmt.Errorf("chip values must be positive, got %d", v)
		}
	}
	// Cell values must sit well above chip values, and this is a 20-team
	// effect. Only the team(s) who WROTE the winning answer take the cell, so
	// with a full room most teams earn nothing from that channel in most
	// rounds and betting is the only income they reliably have. If cells were
	// worth what chips are, the quiz would be decoration on a betting game.
	if cheapest(s.CellValues) < 2*dearest(s.TokenValues) {
		return fmt.Errorf("the cheapest cell (%d) should be worth at least twice the biggest chip (%d), "+
			"or writing the winning answer stops mattering",
			cheapest(s.CellValues), dearest(s.TokenValues))
	}
	for name, v := range map[string]int{
		"answer": s.AnswerSeconds, "reveal": s.RevealSeconds, "betting": s.BetSeconds,
	} {
		if v < minPhaseSeconds || v > maxPhaseSeconds {
			return fmt.Errorf("the %s timer must be %d to %d seconds, got %d",
				name, minPhaseSeconds, maxPhaseSeconds, v)
		}
	}
	return nil
}

func cheapest(vs []int) int {
	out := vs[0]
	for _, v := range vs {
		out = min(out, v)
	}
	return out
}

func dearest(vs []int) int {
	out := vs[0]
	for _, v := range vs {
		out = max(out, v)
	}
	return out
}
