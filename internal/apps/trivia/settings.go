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
	// minRevealSeconds is lower than every other floor because the reveal is
	// not think time. It is a DEAL: the cards fly in, the room reads five
	// numbers, betting opens. Three seconds is enough to deal a hand.
	minRevealSeconds = 3
	// maxGraceSeconds bounds the beat after everyone is in. A minute of it
	// would not be a grace, it would be the phase.
	maxGraceSeconds = 60
)

// DefaultSettings is the shipped game: 5 categories x 2 rows at $100/$200,
// ten questions, two chips at $100/$200, and a final.
//
// CELLS AND CHIPS ARE THE SAME SIZE ON PURPOSE, which makes betting the
// larger half of the game. Only the table that WROTE the winning answer takes
// a cell, and with a full room that is nobody at most tables most rounds --
// whereas putting a chip on the right answer is something every table does
// every round. Reading the room is the skill this game is about; the
// questions are the raw material for that judgement rather than the
// scoreboard themselves.
//
// (An earlier default had cells at five times the chips, which made writing
// the winning answer worth about five good bets. That is the Jeopardy
// weighting, and it is not what this game is for.)
//
// Board shape is deliberately not Jeopardy's 30-clue one. Here EVERY team
// types an answer, watches a reveal and places bets, so a question costs
// about three minutes end to end. Ten questions is roughly half an hour of
// board plus a lobby and a final -- a bar game people finish.
func DefaultSettings() Settings {
	return Settings{
		BoardRows: 2, BoardColumns: 5,
		CellValues: []int{100, 200}, TokenValues: []int{100, 200},
		FinalWager: true, AnswerSeconds: 60, RevealSeconds: 0, BetSeconds: 45,
		// Thirty seconds to commit a number against nothing but a category.
		// Shorter than the answer clock on purpose: there is nothing to work
		// out, only a nerve to settle, and a long blind-bet clock is dead air
		// in a bar.
		WagerSeconds: 30,
		// Five seconds for the last table to look at what it just did. See
		// maybeCloseEarly.
		GraceSeconds: 5,
		// No reruns. A question the room has already been asked is not a
		// question, and the regulars are exactly the people who notice. A
		// host whose bank has run thin can turn this on per game; deleting
		// an old night gives its questions back either way.
		RepeatQuestions: false,
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
	if s.BetSeconds == 0 {
		s.BetSeconds = d.BetSeconds
	}
	if s.WagerSeconds == 0 {
		s.WagerSeconds = d.WagerSeconds
	}
	// RevealSeconds is NOT filled in either: zero means the deal beat is
	// skipped and the cards open straight into betting, which is the shipped
	// default -- the room reads the cards while it bets.
	// GraceSeconds is deliberately NOT filled in. Zero is a real setting here
	// -- it means "close the instant the last table is in", the behaviour the
	// game shipped with -- so treating it as "unset" would make that choice
	// unexpressable. RepeatQuestions is the same shape and needs no line at
	// all: false is both the zero value and the default.
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
	for name, v := range map[string]int{
		"answer": s.AnswerSeconds, "reveal": s.RevealSeconds, "betting": s.BetSeconds,
		"wager": s.WagerSeconds,
	} {
		// The reveal alone gets a lower floor: it is a deal, not a think --
		// and zero is legal there, meaning no deal beat at all: the question
		// closes straight into betting and the room reads the cards while
		// it bets.
		floor := minPhaseSeconds
		if name == "reveal" {
			if v == 0 {
				continue
			}
			floor = minRevealSeconds
		}
		if v < floor || v > maxPhaseSeconds {
			return fmt.Errorf("the %s timer must be %d to %d seconds, got %d",
				name, floor, maxPhaseSeconds, v)
		}
	}
	// Zero is legal and means no grace at all, so this is the one timer whose
	// range starts at the bottom.
	if s.GraceSeconds < 0 || s.GraceSeconds > maxGraceSeconds {
		return fmt.Errorf("the grace after everyone is in must be 0 to %d seconds, got %d",
			maxGraceSeconds, s.GraceSeconds)
	}
	return nil
}
