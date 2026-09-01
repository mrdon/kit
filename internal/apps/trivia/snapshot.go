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

	Teams     []SnapTeam
	Board     []SnapCell
	Round     *SnapRound
	Slots     []SnapSlot
	Bets      []SnapBet
	Standings map[uuid.UUID]int

	// Scoring is non-nil only once the round has been scored. It is a
	// separate type rather than a set of fields on Snapshot precisely so the
	// correct answer cannot be populated early by accident: there is no
	// assignment that half-fills it.
	Scoring *SnapScoring

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

// BoardComplete reports whether every cell has been played, which is what
// ends the board and sends the game to the final or the podium.
func (s *Snapshot) BoardComplete() bool {
	if len(s.Board) == 0 {
		return false
	}
	for _, c := range s.Board {
		if !c.Played {
			return false
		}
	}
	return true
}
