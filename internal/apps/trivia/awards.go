package trivia

import (
	"sort"
	"time"

	"github.com/google/uuid"
)

// Honourable mentions: the awards the room sees just before the podium.
//
// This file and awards_pool.go are PURE, the same bargain scoring.go makes.
// No database, no context, no SQL -- an AwardInput goes in and a handful of
// resolved awards come out, so the whole pool is testable as a table of
// fixtures with no game running.
//
// The design problem these solve is that a pub quiz has one winner and
// twenty tables, and the other nineteen watch someone else's night. The
// shape borrowed from shooter games is that the POOL is what guarantees
// coverage: compute far more awards than there are slots, then hand out the
// slots one per table. Three fixed awards would collide with each other on a
// small night; fifteen candidates for five slots almost never do.
//
// Which is also why most of the pool measures TEMPERAMENT rather than skill.
// Skill awards stack -- the table that knows things wins every performance
// stat there is -- while how hard a table backs itself is uncorrelated with
// whether it is right, so those can land anywhere in the standings.

// MaxAwards is how many mentions the podium will show. The count floats
// below it: with four tables the pool will not find five distinct winners,
// and a short list is the right answer rather than a padded one.
const MaxAwards = 5

// minAwardRounds is the participation floor. A table that walked in at
// question nine has a two-round sample, and every average over it is noise
// -- it would win "closest all night" on one lucky guess. Clamped against
// the length of the night so a genuinely short game still gives out awards.
const minAwardRounds = 3

// Award is one honourable mention, resolved to a table.
type Award struct {
	Key      string
	Title    string
	TeamID   uuid.UUID
	TeamName string
	// Detail is the straight line under the title. The title carries
	// whatever joke there is; this states the fact and stops. See the
	// writing-copy skill's humour budget.
	Detail string
}

// AwardTeam is one table as the pool sees it.
type AwardTeam struct {
	ID           uuid.UUID
	Name         string
	EligibleFrom int
}

// AwardAnswer is what one table typed in one round.
type AwardAnswer struct {
	TeamID      uuid.UUID
	Value       float64
	SubmittedAt time.Time
}

// AwardBet is one chip, against the card it landed on.
type AwardBet struct {
	TeamID uuid.UUID
	SlotID uuid.UUID
	Amount int
}

// AwardSlot is one revealed card. Value is nil on the pseudo-slot, which
// nobody wrote -- so a chip there is on nobody's answer, which several of
// the awards care about.
type AwardSlot struct {
	ID      uuid.UUID
	Value   *float64
	TeamIDs []uuid.UUID
}

// AwardRound is one played round, with everything the pool reads off it.
type AwardRound struct {
	Ordinal       int
	IsFinal       bool
	Correct       float64
	CorrectText   string
	WinningSlotID uuid.UUID
	Answers       []AwardAnswer
	Bets          []AwardBet
	Slots         []AwardSlot
	Deltas        map[uuid.UUID]ScoreDelta
}

// AwardInput is the whole night, assembled once.
type AwardInput struct {
	Teams  []AwardTeam
	Rounds []AwardRound
}

// awardScore is one table's standing in one award, with the line that would
// run under the title if this table won it.
type awardScore struct {
	Value  float64
	Detail string
}

// awardDef is one candidate award.
//
// Selection order and display order are deliberately different fields. The
// pool is walked in slice order to CHOOSE, best-and-most-reliably-distinct
// first; the chosen few are then sorted by Display to SHOW, which runs
// gentle to funny so the last card before the crown is the one that gets the
// biggest laugh.
type awardDef struct {
	Key     string
	Title   string
	Higher  bool
	Display int
	Eval    func(in AwardInput, q qualifier) map[uuid.UUID]awardScore
}

// Awards resolves the pool and picks the mentions to show.
func Awards(in AwardInput) []Award {
	q := newQualifier(in)
	pool := make([]Award, 0, len(awardPool))
	order := map[string]int{}
	for _, def := range awardPool {
		order[def.Key] = def.Display
		if a, ok := resolve(def, in, q); ok {
			pool = append(pool, a)
		}
	}
	out := pickDistinct(pool, MaxAwards)
	sort.SliceStable(out, func(i, j int) bool {
		return order[out[i].Key] < order[out[j].Key]
	})
	return out
}

// resolve turns one definition into its winner, or reports that nothing
// qualified.
//
// The winner is found by walking in.Teams IN ORDER rather than by ranging
// the score map. Ties here are common -- two tables that both answered every
// round, both backed themselves every time -- and Go randomises map
// iteration, so a map range would show a different winner on every reload of
// a screen whose data cannot change. Join order is stable, and strict > ...
// leaves the first table to have joined holding the tie.
func resolve(def awardDef, in AwardInput, q qualifier) (Award, bool) {
	scores := def.Eval(in, q)
	var best *AwardTeam
	var bestScore awardScore
	for i := range in.Teams {
		s, ok := scores[in.Teams[i].ID]
		if !ok {
			continue
		}
		if best == nil || better(s.Value, bestScore.Value, def.Higher) {
			best, bestScore = &in.Teams[i], s
		}
	}
	if best == nil {
		return Award{}, false
	}
	return Award{
		Key: def.Key, Title: def.Title,
		TeamID: best.ID, TeamName: best.Name, Detail: bestScore.Detail,
	}, true
}

func better(a, b float64, higher bool) bool {
	if higher {
		return a > b
	}
	return a < b
}

// pickDistinct is the one-award-per-table rule: walk the resolved pool in
// priority order and take an award only when its winner is not already
// holding one.
//
// Running out is fine and is not padded around. Four tables yield three or
// four mentions, which is the honest length for a small room -- repeating a
// table to reach five would undo the entire point.
func pickDistinct(pool []Award, max int) []Award {
	out := make([]Award, 0, max)
	taken := map[uuid.UUID]bool{}
	for _, a := range pool {
		if len(out) >= max {
			break
		}
		if taken[a.TeamID] {
			continue
		}
		taken[a.TeamID] = true
		out = append(out, a)
	}
	return out
}
