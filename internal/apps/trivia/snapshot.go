package trivia

import (
	"time"

	"github.com/google/uuid"
)

// Snapshot is the complete state of one game at one instant. It is what the
// broker fans out, and every SSE frame on every surface is projected from
// one of these.
//
// It carries the correct answer by necessity -- the host console shows it
// throughout, and scoring needs it -- which makes projection.go the only
// thing standing between it and twenty phones. See
// TestProjectionsNeverLeakTheAnswer.
type Snapshot struct {
	GameID       uuid.UUID
	TenantID     uuid.UUID
	Name         string
	Title        string
	Phase        Phase
	StateVersion int64

	// ServerNow and Deadline are absolute; clients derive a per-frame skew
	// and tick the countdown locally. Countdown ticks are never sent over
	// SSE -- that would put the clock on bar wifi.
	ServerNow time.Time
	Deadline  *time.Time

	FinalWager  bool
	TokenValues []int
	CellValues  []int
	BoardRows   int
	BoardCols   int
	// BoardRound is the board in play, zero-based, and BoardRounds is how
	// many the night holds. Both ride on every frame so the TV can say
	// "round 2 of 2" and the phone knows its chips just doubled.
	BoardRound  int
	BoardRounds int
	// CellsTotal and CellsPlayed count the WHOLE NIGHT, every round of it,
	// because Board carries only the round in play. Anything answering "how
	// far through are we" wants these -- counting Board instead reports "0 of
	// 10 done" during the break, which is both wrong and demoralising.
	CellsTotal  int
	CellsPlayed int

	// PickerTeamID is the table whose pick the next category is, and
	// PickerReason is why it is theirs. Both ride on EVERY surface's frame --
	// the TV announces it, the phone tells one table it is their turn, and
	// the console reads the sentence out. Nil before the game starts.
	PickerTeamID *uuid.UUID
	PickerReason PickerReason

	Teams     []SnapTeam
	Board     []SnapCell
	Round     *SnapRound
	Slots     []SnapSlot
	Bets      []SnapBet
	Standings map[uuid.UUID]int

	// LastRound is the round scored most recently, which is NOT the same
	// thing as Scoring: it survives "next" clearing the current round, and
	// is what the board phase is told. Host frame only -- see
	// SnapLastRound.
	LastRound *SnapLastRound

	// Scoring is non-nil only once the round has been scored. It is a
	// separate type rather than a set of fields on Snapshot precisely so the
	// correct answer cannot be populated early by accident: there is no
	// assignment that half-fills it.
	Scoring *SnapScoring

	// Awards are the honourable mentions, and they are resolved ONLY on the
	// podium -- see awards.go. Nothing else would be served by carrying them
	// earlier: they are read off frozen data, they cost four queries, and a
	// half-played night has no honourable mentions in it.
	Awards []Award

	// PublisherID identifies the process that produced this snapshot, so a
	// relayed message coming back around is recognised and dropped rather
	// than re-fanned locally.
	PublisherID string
}

// SnapTeam is one table as every surface sees it. Answered/Locked/ChipsPlaced
// are the progress pips: the room can see WHICH table is holding everyone up,
// which a bare "12 of 20" cannot show.
type SnapTeam struct {
	ID          uuid.UUID
	Name        string
	Score       int
	Eligible    bool
	Answered    bool
	StakeLocked bool
	ChipsPlaced int

	// EligibleFrom is the ordinal this table is in from, carried raw so a
	// phone can be told WHICH question it joins on rather than merely that
	// it is sitting this one out. Eligible is that same fact compared
	// against the round in play, and is the one every other surface wants.
	EligibleFrom int

	// Stake is the final's locked wager, read off the wager row, and it is
	// the one number in here that only ONE surface may ever see. StakeLocked
	// is the public tell -- the room knows a table has committed -- while the
	// amount rides down to that table's own phone and nowhere else: not
	// knowing whether the leader defended or sat out is most of the tension.
	// Nil outside a final, and nil for a table that has not locked one in yet.
	Stake *int
}

// SnapCell is one board tile.
type SnapCell struct {
	ID     uuid.UUID
	Col    int
	Row    int
	Topic  string
	Points int
	Played bool
}

// SnapRound is the question in play.
type SnapRound struct {
	ID      uuid.UUID
	IsFinal bool
	Ordinal int
	Points  int
	Text    string

	// Topic is the category. It is the ONE thing about the question that the
	// wager phase may show -- the room bets on a word, not on a prompt -- so
	// it has to be separable from Text all the way out to the wire.
	Topic string

	// CorrectValue and CorrectText are host-only until scoring. They live
	// here because the snapshot is the single source every surface projects
	// from; withholding is projection's job, not assembly's.
	CorrectValue float64
	CorrectText  string

	// AnsweredCount over EligibleCount is the "12 OF 20 IN" strip, and the
	// early-close test. Eligible excludes teams that joined after this round
	// opened -- without that the denominator grows mid-question, the counter
	// ticks backwards, and the everyone's-in close never fires.
	AnsweredCount int
	EligibleCount int
}

// SnapSlot is one revealed card. Values and team names appear from reveal
// onward; before that the slice is empty.
type SnapSlot struct {
	ID        uuid.UUID
	Position  int
	Value     *float64
	Label     string
	TeamIDs   []uuid.UUID
	TeamNames []string
	Pot       int
}

// SnapBet is one chip.
type SnapBet struct {
	TeamID     uuid.UUID
	TokenIndex int
	Amount     int
	SlotID     uuid.UUID
}

// SnapScoring is the result of a scored round -- and the gate the answer
// passes through on its way to the public surfaces.
type SnapScoring struct {
	CorrectValue  float64
	CorrectText   string
	WinningSlotID *uuid.UUID
	Deltas        map[uuid.UUID]ScoreDelta
}

// SnapLastRound is the previous question's result, carried into the phases
// where nothing is in play.
//
// The host needs it at exactly one moment and needs it badly: the round has
// been scored, they have pressed next, the game is sitting on the board, and
// the table that wrote the winning answer picks the next category. Without
// this the console has forgotten who that was.
//
// It is projected into the HOST frame only. The answer in a scored round is
// already public, so this is not a withholding rule -- it is that the TV and
// the phones have moved on to the next question and would only be confused
// by the last one.
type SnapLastRound struct {
	LastRoundSummary
	// Deltas is the per-team movement for that round, same shape as
	// SnapScoring.Deltas, so the host can read the swing back out.
	Deltas map[uuid.UUID]ScoreDelta
}

// ScoreDelta is one team's movement for the round, split so the phone can
// show "you wrote the winner" and "your chip paid" as different things.
type ScoreDelta struct {
	BoardPoints int
	BetDelta    int
}

// Total is what the leaderboard counts up.
func (d ScoreDelta) Total() int { return d.BoardPoints + d.BetDelta }

// TeamByID finds a team in the snapshot, or nil.
func (s *Snapshot) TeamByID(id uuid.UUID) *SnapTeam {
	for i := range s.Teams {
		if s.Teams[i].ID == id {
			return &s.Teams[i]
		}
	}
	return nil
}

// DeadlineMillis is the absolute epoch-ms deadline, or 0 for a phase that
// waits on a human rather than a clock.
func (s *Snapshot) DeadlineMillis() int64 {
	if s.Deadline == nil {
		return 0
	}
	return s.Deadline.UnixMilli()
}
