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
}

func commonOf(s *Snapshot) wireCommon {
	return wireCommon{
		Version: s.StateVersion, Game: s.Name, Title: s.Title, Phase: string(s.Phase),
		ServerNow: s.ServerNow.UnixMilli(), DeadlineMs: s.DeadlineMillis(),
		FinalWager: s.FinalWager,
	}
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
type wireRound struct {
	ID       string `json:"id"`
	IsFinal  bool   `json:"isFinal"`
	Ordinal  int    `json:"ordinal"`
	Points   int    `json:"points"`
	Text     string `json:"text"`
	Answered int    `json:"answered"`
	Eligible int    `json:"eligible"`
}

// DisplayFrame is what the TV sees.
type DisplayFrame struct {
	wireCommon
	Teams   []wireTeam   `json:"teams"`
	Board   []wireCell   `json:"board"`
	Round   *wireRound   `json:"round"`
	Slots   []wireSlot   `json:"slots"`
	Scoring *wireScoring `json:"scoring"`
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
	You     *wireYou     `json:"you"`
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
}

type wireOwnChip struct {
	TokenIndex int    `json:"tokenIndex"`
	Amount     int    `json:"amount"`
	SlotID     string `json:"slotId"`
}

// HostFrame is the console's view. It carries the correct answer in every
// phase -- the host is reading it out and adjudicating nothing, so hiding it
// would be theatre with a cost.
type HostFrame struct {
	wireCommon
	Teams    []wireTeam   `json:"teams"`
	Board    []wireCell   `json:"board"`
	Round    *wireRound   `json:"round"`
	Slots    []wireSlot   `json:"slots"`
	Scoring  *wireScoring `json:"scoring"`
	Answer   *wireAnswer  `json:"answer"`
	Tokens   []int        `json:"tokens"`
	Progress wireProgress `json:"progress"`
}

// wireAnswer is the host-only correct answer.
type wireAnswer struct {
	Value float64 `json:"value"`
	Text  string  `json:"text"`
}

// wireProgress is the host's at-a-glance board state.
type wireProgress struct {
	CellsPlayed int  `json:"cellsPlayed"`
	CellsTotal  int  `json:"cellsTotal"`
	FinalPlayed bool `json:"finalPlayed"`
}

// revealed reports whether the cards may carry their values and team names
// yet. Before reveal they must not: a phone that could read the field early
// would know exactly what to bet on.
func revealed(s *Snapshot) bool {
	switch s.Phase {
	case PhaseReveal, PhaseBetting, PhaseScoring, PhasePodium:
		return true
	case PhaseSetup, PhaseLobby, PhaseBoard, PhaseQuestion:
		return false
	}
	return false
}

// questionVisible reports whether the prompt may be shown. It is on screen in
// the bar from the moment the cell is picked, so every phase from question
// onward carries it.
func questionVisible(s *Snapshot) bool {
	switch s.Phase {
	case PhaseQuestion, PhaseReveal, PhaseBetting, PhaseScoring, PhasePodium:
		return true
	case PhaseSetup, PhaseLobby, PhaseBoard:
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

// ProjectHost builds the console's frame, answer included.
func ProjectHost(s *Snapshot) HostFrame {
	f := HostFrame{
		wireCommon: commonOf(s),
		Teams:      publicTeams(s),
		Board:      publicBoard(s),
		Round:      publicRound(s),
		Scoring:    publicScoring(s),
		Tokens:     s.TokenValues,
	}
	// The host sees the cards from the moment they exist, and the answer in
	// every phase.
	f.Slots = allSlots(s)
	if s.Round != nil {
		f.Answer = &wireAnswer{Value: s.Round.CorrectValue, Text: s.Round.CorrectText}
	}
	for _, c := range s.Board {
		f.Progress.CellsTotal++
		if c.Played {
			f.Progress.CellsPlayed++
		}
	}
	if s.Round != nil && s.Round.IsFinal {
		f.Progress.FinalPlayed = true
	}
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
func publicRound(s *Snapshot) *wireRound {
	if s.Round == nil || !questionVisible(s) {
		return nil
	}
	return &wireRound{
		ID: s.Round.ID.String(), IsFinal: s.Round.IsFinal, Ordinal: s.Round.Ordinal,
		Points: s.Round.Points, Text: s.Round.Text,
		Answered: s.Round.AnsweredCount, Eligible: s.Round.EligibleCount,
	}
}

func publicSlots(s *Snapshot) []wireSlot {
	if !revealed(s) {
		return []wireSlot{}
	}
	return allSlots(s)
}

func allSlots(s *Snapshot) []wireSlot {
	out := make([]wireSlot, 0, len(s.Slots))
	for _, sl := range s.Slots {
		w := wireSlot{
			ID: sl.ID.String(), Pos: sl.Position, Value: sl.Value,
			Label: sl.Label, Teams: sl.TeamNames, Pot: sl.Pot, Chips: []wireChip{},
		}
		if w.Teams == nil {
			w.Teams = []string{}
		}
		for _, b := range s.Bets {
			if b.SlotID == sl.ID {
				name := ""
				if t := s.TeamByID(b.TeamID); t != nil {
					name = t.Name
				}
				w.Chips = append(w.Chips, wireChip{Team: name, Amount: b.Amount})
			}
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
