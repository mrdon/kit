package trivia

import "github.com/google/uuid"

// Three surfaces, three projections, three endpoints -- deliberately NOT one
// endpoint with a query parameter. A shared endpoint filtered by a param is
// one typo away from serving the correct answer to twenty phones mid-question,
// and that typo would look like nothing in review.
//
// The broker fans out the typed *Snapshot; each connection projects and
// marshals for itself.

// wireCommon is what every surface gets: enough to render a countdown and
// know what phase it is in.
//
// DeadlineMs and ServerNow ride on every frame so the client can compute
// skew = serverNow - Date.now() and render deadlineMs - (Date.now() + skew),
// ticked locally at 100ms. Taking the latest sample folds one-way delay in as
// a conservative bias, so the phone runs slightly AHEAD of the server -- the
// right direction to be wrong in. Countdown ticks are NEVER sent over SSE;
// that would put the clock on bar wifi.
type wireCommon struct {
	Version    int64  `json:"version"`
	Game       string `json:"game"`
	Title      string `json:"title"`
	Phase      string `json:"phase"`
	ServerNow  int64  `json:"serverNow"`
	DeadlineMs int64  `json:"deadlineMs"`
	FinalWager bool   `json:"finalWager"`
	// RoundNumber and RoundCount are the night's position as a human says it
	// out loud: one-based, and THE FINAL COUNTS AS A ROUND. Two boards plus a
	// final is "round 1 of 3".
	//
	// Named apart from Snapshot.BoardRound/BoardRounds on purpose. Those are
	// zero-based and count boards only; these are one-based and count the
	// final. The two lived under the same name once and both readers added
	// one to an already-one-based number, so a two-round night announced
	// "Round 3 of 2" on the wall.
	RoundNumber int `json:"roundNumber"`
	RoundCount  int `json:"roundCount"`
	// IsFinal says the round in play IS the final, so a surface can print the
	// word rather than its number.
	IsFinal bool `json:"isFinal"`
}

// roundNumbersOf places the night for a human: which round of how many, with
// the final counted as the last one.
func roundNumbersOf(s *Snapshot) (number, count int, isFinal bool) {
	count = max(s.BoardRounds, 1)
	if s.FinalWager {
		count++
	}
	number = s.BoardRound + 1
	if s.Round != nil && s.Round.IsFinal {
		// The final is the last round by definition, whatever the boards did.
		return count, count, true
	}
	return min(number, count), count, false
}

func commonOf(s *Snapshot) wireCommon {
	c := wireCommon{
		Version: s.StateVersion, Game: s.Name, Title: s.Title, Phase: string(s.Phase),
		ServerNow: s.ServerNow.UnixMilli(), DeadlineMs: s.DeadlineMillis(),
		FinalWager: s.FinalWager,
	}
	c.RoundNumber, c.RoundCount, c.IsFinal = roundNumbersOf(s)
	return c
}

// wireTeam is a table as the public surfaces see it. No answer, no stake
// amount -- only whether one has landed.
type wireTeam struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Score       int    `json:"score"`
	Eligible    bool   `json:"eligible"`
	Answered    bool   `json:"answered"`
	StakeLocked bool   `json:"stakeLocked"`
	ChipsPlaced int    `json:"chipsPlaced"`
}

// wireCell is a board tile.
type wireCell struct {
	ID     string `json:"id"`
	Col    int    `json:"col"`
	Row    int    `json:"row"`
	Topic  string `json:"topic"`
	Points int    `json:"points"`
	Played bool   `json:"played"`
}

// wireSlot is a revealed card. Values and team names appear only from reveal
// onward -- before that this slice is empty on every public surface.
type wireSlot struct {
	ID    string     `json:"id"`
	Pos   int        `json:"pos"`
	Value *float64   `json:"value"`
	Label string     `json:"label"`
	Teams []string   `json:"teams"`
	Pot   int        `json:"pot"`
	Chips []wireChip `json:"chips"`
}

type wireChip struct {
	Team   string `json:"team"`
	Amount int    `json:"amount"`
}

// wirePicker is the table whose pick the next category is. Nil before the
// game starts, and never nil after -- the server chooses, always, so no
// surface has to invent a fallback sentence.
//
// It rides on ALL THREE frames rather than just the console's, because all
// three say something about it: the TV spins a wheel for the draw and then
// carries a callout on the board, the phone tells one table it is their turn,
// and the console gives the host the sentence to read out. One field, three
// phrasings, no room for them to disagree about who it is.
type wirePicker struct {
	TeamID string `json:"teamId"`
	Name   string `json:"name"`
}

// publicPicker resolves the stored team id against the room. A picker whose
// team has left resolves to nil rather than to a blank name.
func publicPicker(s *Snapshot) *wirePicker {
	if s.PickerTeamID == nil {
		return nil
	}
	t := s.TeamByID(*s.PickerTeamID)
	if t == nil {
		return nil
	}
	return &wirePicker{TeamID: t.ID.String(), Name: t.Name}
}

// pickerReasonOf is the reason as the wire carries it, and it is blanked when
// there is no picker to attach it to -- a reason with nobody holding it is
// half a sentence a surface would render anyway.
func pickerReasonOf(s *Snapshot) string {
	if publicPicker(s) == nil {
		return ""
	}
	return string(s.PickerReason)
}

// wireScoring is nilable and a DISTINCT TYPE rather than a set of fields on
// the frame, so the correct answer cannot be populated early by accident:
// there is no assignment that half-fills it.
type wireScoring struct {
	CorrectValue float64        `json:"correctValue"`
	CorrectText  string         `json:"correctText"`
	WinningSlot  string         `json:"winningSlot"`
	Deltas       map[string]int `json:"deltas"`
	BoardPoints  map[string]int `json:"boardPoints"`
	BetDeltas    map[string]int `json:"betDeltas"`
}

// wireRound is the question in play, WITHOUT its answer.
//
// Text and Category are separately withheld, which is the wager phase's whole
// requirement: the room is shown a category and a clock and must not be able
// to read the prompt out of a frame it already has. So during `wager` this
// struct ships with Category set and Text empty, and the prompt does not exist
// on any public surface until the phase closes.
type wireRound struct {
	ID       string `json:"id"`
	IsFinal  bool   `json:"isFinal"`
	Ordinal  int    `json:"ordinal"`
	Points   int    `json:"points"`
	Text     string `json:"text"`
	Category string `json:"category"`
	Answered int    `json:"answered"`
	Eligible int    `json:"eligible"`
}

// DisplayFrame is what the TV sees.
//
// Tokens is here for one reason: the wall's "N OF M TABLES IN" tally cannot
// say whether a table is finished without knowing how many chips a table
// holds. Lacking it the TV assumed one, and counted a table that had placed
// the first of two as in.
type DisplayFrame struct {
	wireCommon
	Teams   []wireTeam   `json:"teams"`
	Board   []wireCell   `json:"board"`
	Round   *wireRound   `json:"round"`
	Slots   []wireSlot   `json:"slots"`
	Scoring *wireScoring `json:"scoring"`
	Tokens  []int        `json:"tokens"`
	// Picker and PickerReason are who picks the next category and why. The
	// board screen announces it, and the first one of the night is what the
	// wheel spins for.
	Picker       *wirePicker `json:"picker"`
	PickerReason string      `json:"pickerReason"`
}

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

// revealed reports whether the cards may carry their values and team names
// yet. Before reveal they must not: a phone that could read the field early
// would know exactly what to bet on.
func revealed(s *Snapshot) bool {
	switch s.Phase {
	case PhaseReveal, PhaseBetting, PhaseScoring, PhasePodium:
		return true
	case PhaseSetup, PhaseLobby, PhaseBoard, PhaseIntermission, PhaseWager, PhaseQuestion:
		return false
	}
	return false
}

// roundVisible reports whether there is anything at all to say about the round
// in play. True one phase earlier than questionVisible, because `wager` shows
// the category, the ordinal and the clock with the prompt still withheld.
func roundVisible(s *Snapshot) bool {
	return questionVisible(s) || s.Phase == PhaseWager
}

// questionVisible reports whether the prompt may be shown. It is on screen in
// the bar from the moment the cell is picked, so every phase from question
// onward carries it.
func questionVisible(s *Snapshot) bool {
	switch s.Phase {
	case PhaseQuestion, PhaseReveal, PhaseBetting, PhaseScoring, PhasePodium:
		return true
	case PhaseSetup, PhaseLobby, PhaseBoard, PhaseIntermission, PhaseWager:
		// WAGER IS THE LOAD-BEARING ONE. The final's prompt must not reach a
		// phone or a TV while the room is still committing money against the
		// category, and this is the line that stops it.
		return false
	}
	return false
}

// ProjectDisplay builds the TV's frame.
func ProjectDisplay(s *Snapshot) DisplayFrame {
	return DisplayFrame{
		wireCommon: commonOf(s),
		Teams:      publicTeams(s),
		Board:      publicBoard(s),
		Round:      publicRound(s),
		Slots:      publicSlots(s),
		Scoring:    publicScoring(s),
		Tokens:     s.TokenValues,

		Picker:       publicPicker(s),
		PickerReason: pickerReasonOf(s),
	}
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

func publicTeams(s *Snapshot) []wireTeam {
	out := make([]wireTeam, 0, len(s.Teams))
	for _, t := range s.Teams {
		out = append(out, wireTeam{
			ID: t.ID.String(), Name: t.Name, Score: t.Score, Eligible: t.Eligible,
			Answered: t.Answered, StakeLocked: t.StakeLocked, ChipsPlaced: t.ChipsPlaced,
		})
	}
	return out
}

func publicBoard(s *Snapshot) []wireCell {
	out := make([]wireCell, 0, len(s.Board))
	for _, c := range s.Board {
		out = append(out, wireCell{
			ID: c.ID.String(), Col: c.Col, Row: c.Row,
			Topic: c.Topic, Points: c.Points, Played: c.Played,
		})
	}
	return out
}

// publicRound carries the prompt but never the answer. The answer lives in
// SnapRound by necessity; this function is where it stops.
//
// It is also where the PROMPT stops during `wager`: the category goes out, the
// question does not, and the field is left empty rather than the whole round
// being withheld -- the wager screen needs the ordinal, the category and the
// eligible count to render at all.
func publicRound(s *Snapshot) *wireRound {
	if s.Round == nil || !roundVisible(s) {
		return nil
	}
	w := &wireRound{
		ID: s.Round.ID.String(), IsFinal: s.Round.IsFinal, Ordinal: s.Round.Ordinal,
		Points: s.Round.Points, Category: s.Round.Topic,
		Answered: s.Round.AnsweredCount, Eligible: s.Round.EligibleCount,
	}
	if questionVisible(s) {
		w.Text = s.Round.Text
	}
	return w
}

// betsVisible reports whether other tables' chips may be shown.
//
// FROM BETTING ONWARD. This used to withhold them until the phase closed, on
// the argument that a table still deciding should not see where the room had
// committed. Played in a bar, that argument lost: a wall of cards with
// nothing on them reads as a screen that has frozen, and the moment the
// chips are worth watching — one landing on the long shot, three piling onto
// the favourite, a table lifting a chip and moving it with ten seconds left —
// is exactly the moment nobody could see. Chips land on the TV as they are
// placed. Yes, the last table to bet knows more than the first; that is the
// same information any table gets by looking around the room, and it is
// worth the beat.
//
// Still hidden before betting opens: during the question and the reveal
// there is nothing placed yet, and the cards must not carry a pot from a
// previous life.
func betsVisible(s *Snapshot) bool {
	switch s.Phase {
	case PhaseBetting, PhaseScoring, PhasePodium:
		return true
	case PhaseSetup, PhaseLobby, PhaseBoard, PhaseIntermission, PhaseWager, PhaseQuestion, PhaseReveal:
		return false
	}
	return false
}

func publicSlots(s *Snapshot) []wireSlot {
	if !revealed(s) {
		return []wireSlot{}
	}
	return slotsWith(s, betsVisible(s))
}

// slotsWith builds the cards, optionally carrying the chips on them.
func slotsWith(s *Snapshot, withBets bool) []wireSlot {
	out := make([]wireSlot, 0, len(s.Slots))
	for _, sl := range s.Slots {
		w := wireSlot{
			ID: sl.ID.String(), Pos: sl.Position, Value: sl.Value,
			Label: sl.Label, Teams: sl.TeamNames, Pot: sl.Pot, Chips: []wireChip{},
		}
		if w.Teams == nil {
			w.Teams = []string{}
		}
		if withBets {
			for _, b := range s.Bets {
				if b.SlotID == sl.ID {
					name := ""
					if t := s.TeamByID(b.TeamID); t != nil {
						name = t.Name
					}
					w.Chips = append(w.Chips, wireChip{Team: name, Amount: b.Amount})
				}
			}
		} else {
			// Pot too: the total on a card is the same tell as the chips.
			w.Pot = 0
		}
		out = append(out, w)
	}
	return out
}

// publicScoring is the gate the answer passes through on its way to the
// public surfaces, and it is the only one.
func publicScoring(s *Snapshot) *wireScoring {
	if s.Scoring == nil {
		return nil
	}
	w := &wireScoring{
		CorrectValue: s.Scoring.CorrectValue, CorrectText: s.Scoring.CorrectText,
		Deltas: map[string]int{}, BoardPoints: map[string]int{}, BetDeltas: map[string]int{},
	}
	if s.Scoring.WinningSlotID != nil {
		w.WinningSlot = s.Scoring.WinningSlotID.String()
	}
	for teamID, d := range s.Scoring.Deltas {
		w.Deltas[teamID.String()] = d.Total()
		w.BoardPoints[teamID.String()] = d.BoardPoints
		w.BetDeltas[teamID.String()] = d.BetDelta
	}
	return w
}
