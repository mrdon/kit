package trivia

import "github.com/google/uuid"

// The phone's projection, split from projection.go the way the console's
// already was. Same reason: one file per surface keeps the question "what
// does a phone get to see" answerable by opening one file, and it is the
// question that matters most here -- this is the surface twenty strangers
// are holding during a live question.

// PlayerFrame is what a phone sees: the display's view plus its own team's
// private state, and nothing about anyone else's.
type PlayerFrame struct {
	wireCommon
	Teams   []wireTeam   `json:"teams"`
	Board   []wireCell   `json:"board"`
	Round   *wireRound   `json:"round"`
	Slots   []wireSlot   `json:"slots"`
	Scoring *wireScoring `json:"scoring"`
	Tokens  []int        `json:"tokens"`
	// Picker is how a phone knows it is that table's turn to choose. Public,
	// not private: every phone shows the same name, and only the one whose
	// teamId matches says "it's your pick".
	Picker       *wirePicker `json:"picker"`
	PickerReason string      `json:"pickerReason"`
	You          *wireYou    `json:"you"`
	// Rules come from the server so the phone and the TV cannot tell a room
	// different games. Cheap enough to ride on every frame (a few hundred
	// bytes against a 3-6 KB snapshot) and that way there is no second fetch
	// and no chance of a stale copy.
	Rules []string `json:"rules"`
	// Awards are the honourable mentions. Empty until the podium.
	Awards []wireAward `json:"awards"`
}

// wireYou is one team's own state. Only ever populated for the team whose
// cookie made the request.
type wireYou struct {
	TeamID      string        `json:"teamId"`
	Name        string        `json:"name"`
	Score       int           `json:"score"`
	Answered    bool          `json:"answered"`
	Chips       []wireOwnChip `json:"chips"`
	Stake       *int          `json:"stake"`
	Delta       *int          `json:"delta"`
	WroteWinner bool          `json:"wroteWinner"`

	// Eligible and InFromQuestion are how a latecomer's phone knows to show
	// a waiting screen instead of an answer box. publicTeams carries
	// `eligible` for everybody, but only the table itself gets told WHICH
	// question it is in from -- "you're in from question 7" is a sentence
	// worth reading, and "sitting out" on its own reads like a punishment.
	//
	// InFromQuestion is omitempty because zero is not a question number: a
	// table that is already in has nothing to be told.
	Eligible       bool `json:"eligible"`
	InFromQuestion int  `json:"inFromQuestion,omitempty"`
}

type wireOwnChip struct {
	TokenIndex int    `json:"tokenIndex"`
	Amount     int    `json:"amount"`
	SlotID     string `json:"slotId"`
}

// ProjectPlayer builds one phone's frame. teamID is uuid.Nil for a
// spectator, who gets everything the TV gets and nothing private -- the
// stream must work with no cookie, not 401.
func ProjectPlayer(s *Snapshot, teamID uuid.UUID) PlayerFrame {
	f := PlayerFrame{
		wireCommon: commonOf(s),
		Teams:      publicTeams(s),
		Board:      publicBoard(s),
		Round:      publicRound(s),
		Slots:      publicSlots(s),
		Scoring:    publicScoring(s),
		Tokens:     s.TokenValues,
		Rules:      Rules(s.FinalWager, s.TokenValues),

		Picker:       publicPicker(s),
		PickerReason: pickerReasonOf(s),
		Awards:       publicAwards(s),
	}
	if teamID == uuid.Nil {
		return f
	}
	team := s.TeamByID(teamID)
	if team == nil {
		return f
	}
	you := &wireYou{
		TeamID: teamID.String(), Name: team.Name, Score: team.Score,
		Answered: team.Answered, Chips: []wireOwnChip{},
		Eligible: team.Eligible,
	}
	if !team.Eligible {
		you.InFromQuestion = team.EligibleFrom
	}
	// The final's wager, and this is the ONLY assignment of it anywhere in
	// this file. publicTeams carries stakeLocked and stops there, so the TV
	// and the other nineteen phones know a table has committed without
	// knowing to what -- and the phone that staked it can show the table its
	// own number: on the wager screen, where "Locked in" has to name the
	// amount, and in betting, where its single chip IS the wager.
	if team.Stake != nil {
		stake := *team.Stake
		you.Stake = &stake
	}
	for _, b := range s.Bets {
		if b.TeamID == teamID {
			you.Chips = append(you.Chips, wireOwnChip{
				TokenIndex: b.TokenIndex, Amount: b.Amount, SlotID: b.SlotID.String(),
			})
		}
	}
	if s.Scoring != nil {
		if d, ok := s.Scoring.Deltas[teamID]; ok {
			total := d.Total()
			you.Delta = &total
			you.WroteWinner = d.BoardPoints > 0
		} else {
			zero := 0
			you.Delta = &zero
		}
	}
	f.You = you
	return f
}
