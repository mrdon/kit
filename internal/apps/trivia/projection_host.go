package trivia

// The host's frame, kept apart from the two public ones.
//
// Everything in this file is allowed to know things the room does not -- the
// correct answer in every phase, the chips as they land, the round that has
// already been put away -- so it lives where that is obvious rather than
// interleaved with the projections whose whole job is withholding.

// HostFrame is the console's view. It carries the correct answer in every
// phase -- the host is reading it out and adjudicating nothing, so hiding it
// would be theatre with a cost.
type HostFrame struct {
	wireCommon
	Teams   []wireTeam   `json:"teams"`
	Board   []wireCell   `json:"board"`
	Round   *wireRound   `json:"round"`
	Slots   []wireSlot   `json:"slots"`
	Scoring *wireScoring `json:"scoring"`
	Answer  *wireAnswer  `json:"answer"`
	// LastRound is the previous question's result, and the reason the board
	// phase can still say who picks the next category. Host only.
	LastRound *wireLastRound `json:"lastRound"`
	Tokens    []int          `json:"tokens"`
	Progress  wireProgress   `json:"progress"`
	// Picker is the table that picks the next category, decided by the
	// server. The console's board cue and the recap's pick line both read
	// THIS and never lastRound -- lastRound knows who wrote the winning card
	// and nothing about ties, an empty card, or the first question.
	Picker       *wirePicker `json:"picker"`
	PickerReason string      `json:"pickerReason"`
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

// wireLastRound is the scored round the console remembers after the game has
// moved off it. Winners and winnerIds are parallel arrays in the same order,
// as the query guarantees.
type wireLastRound struct {
	Ordinal      int            `json:"ordinal"`
	IsFinal      bool           `json:"isFinal"`
	Points       int            `json:"points"`
	Text         string         `json:"text"`
	CorrectValue float64        `json:"correctValue"`
	CorrectText  string         `json:"correctText"`
	WinningSlot  string         `json:"winningSlot"`
	WinningLabel string         `json:"winningLabel"`
	Winners      []string       `json:"winners"`
	WinnerIDs    []string       `json:"winnerIds"`
	Deltas       map[string]int `json:"deltas"`
	BoardPoints  map[string]int `json:"boardPoints"`
	BetDeltas    map[string]int `json:"betDeltas"`
}

// ProjectHost builds the console's frame, answer included.
func ProjectHost(s *Snapshot) HostFrame {
	f := HostFrame{
		wireCommon: commonOf(s),
		Teams:      publicTeams(s),
		Board:      publicBoard(s),
		Round:      publicRound(s),
		Scoring:    publicScoring(s),
		LastRound:  hostLastRound(s),
		Tokens:     s.TokenValues,

		Picker:       publicPicker(s),
		PickerReason: pickerReasonOf(s),
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

// allSlots is the host's cards: always with the chips on them, in every
// phase, because knowing who has not placed is half of what the console is
// for.
func allSlots(s *Snapshot) []wireSlot {
	return slotsWith(s, true)
}

// hostLastRound flattens the previous round's summary for the wire. Nil until
// a round has actually been scored, so the console can tell "first question
// of the night" from "the last one had no winner".
func hostLastRound(s *Snapshot) *wireLastRound {
	if s.LastRound == nil {
		return nil
	}
	lr := s.LastRound
	w := &wireLastRound{
		Ordinal: lr.Ordinal, IsFinal: lr.IsFinal, Points: lr.Points, Text: lr.Prompt,
		CorrectValue: lr.AnswerValue, CorrectText: lr.AnswerText,
		WinningLabel: lr.WinningLabel,
		Winners:      append([]string{}, lr.WinnerNames...),
		WinnerIDs:    make([]string, 0, len(lr.WinnerIDs)),
		Deltas:       map[string]int{}, BoardPoints: map[string]int{}, BetDeltas: map[string]int{},
	}
	if lr.WinningSlotID != nil {
		w.WinningSlot = lr.WinningSlotID.String()
	}
	for _, id := range lr.WinnerIDs {
		w.WinnerIDs = append(w.WinnerIDs, id.String())
	}
	for teamID, d := range lr.Deltas {
		w.Deltas[teamID.String()] = d.Total()
		w.BoardPoints[teamID.String()] = d.BoardPoints
		w.BetDeltas[teamID.String()] = d.BetDelta
	}
	return w
}
