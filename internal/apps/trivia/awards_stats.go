package trivia

import (
	"fmt"
	"math"
	"slices"
	"sort"

	"github.com/google/uuid"
)

// The reading helpers the award pool is written against. Every one of them
// is a plain function over an AwardRound, so an award definition stays a
// line or two and the arithmetic lives here where it can be tested once.

// qualifier answers "may this table win an award at all".
//
// Two tables fail it: the one that walked in near the end, whose averages
// are noise, and the one that never typed an answer. Both would otherwise
// win things on a sample of one.
type qualifier struct {
	from   map[uuid.UUID]int
	played map[uuid.UUID]int
	ok     map[uuid.UUID]bool
}

func newQualifier(in AwardInput) qualifier {
	q := qualifier{
		from:   map[uuid.UUID]int{},
		played: map[uuid.UUID]int{},
		ok:     map[uuid.UUID]bool{},
	}
	for _, t := range in.Teams {
		q.from[t.ID] = t.EligibleFrom
	}
	answered := map[uuid.UUID]bool{}
	for _, r := range in.Rounds {
		for _, t := range in.Teams {
			if r.Ordinal >= q.from[t.ID] {
				q.played[t.ID]++
			}
		}
		for _, a := range r.Answers {
			answered[a.TeamID] = true
		}
	}
	// Clamped against the length of the night: a five-question game should
	// still give out awards, and there every table is on a short sample.
	floor := min(minAwardRounds, len(in.Rounds))
	for _, t := range in.Teams {
		q.ok[t.ID] = answered[t.ID] && q.played[t.ID] >= floor && floor > 0
	}
	return q
}

// Eligible reports whether a round counts for a table, which is false for
// every round that ran before they sat down.
func (q qualifier) Eligible(r AwardRound, teamID uuid.UUID) bool {
	return r.Ordinal >= q.from[teamID]
}

// OK reports whether a table may hold an award.
func (q qualifier) OK(teamID uuid.UUID) bool { return q.ok[teamID] }

// answerOf is what one table typed in one round, if it typed anything.
func answerOf(r AwardRound, teamID uuid.UUID) (AwardAnswer, bool) {
	for _, a := range r.Answers {
		if a.TeamID == teamID {
			return a, true
		}
	}
	return AwardAnswer{}, false
}

// slotOf is the card carrying this table's answer.
//
// Cards dedupe by VALUE, so two tables that both wrote 1969 share one -- and
// this returns that shared card for both of them. Every award that asks
// "did they bet on themselves" therefore counts a chip on a number you also
// wrote as backing yourself, which is how it feels at the table.
func slotOf(r AwardRound, teamID uuid.UUID) (AwardSlot, bool) {
	for _, s := range r.Slots {
		if slices.Contains(s.TeamIDs, teamID) {
			return s, true
		}
	}
	return AwardSlot{}, false
}

// wroteWinner reports whether this table's answer took the round.
func wroteWinner(r AwardRound, teamID uuid.UUID) bool {
	s, ok := slotOf(r, teamID)
	return ok && s.ID == r.WinningSlotID
}

// betsOf is every chip one table put down in one round.
func betsOf(r AwardRound, teamID uuid.UUID) []AwardBet {
	out := make([]AwardBet, 0, 2)
	for _, b := range r.Bets {
		if b.TeamID == teamID {
			out = append(out, b)
		}
	}
	return out
}

// onOwnCard reports whether a chip landed on a card this table wrote.
func onOwnCard(r AwardRound, b AwardBet, teamID uuid.UUID) bool {
	s, ok := slotOf(r, teamID)
	return ok && s.ID == b.SlotID
}

// guesses is every number typed in a round, which is what "wild" is judged
// against.
func guesses(r AwardRound) []float64 {
	out := make([]float64, 0, len(r.Answers))
	for _, a := range r.Answers {
		out = append(out, a.Value)
	}
	return out
}

// median of a copy, so the caller's slice keeps its order.
func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	mid := len(s) / 2
	if len(s)%2 == 1 {
		return s[mid]
	}
	return (s[mid-1] + s[mid]) / 2
}

// medianAbsDev is the robust spread: the median distance from the median.
//
// Median and MAD rather than mean and standard deviation because the outlier
// being hunted drags a mean towards itself and then hides inside its own
// standard deviation, which is the exact failure mode of scoring "wildest"
// with a z-score.
func medianAbsDev(xs []float64, med float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	devs := make([]float64, 0, len(xs))
	for _, x := range xs {
		devs = append(devs, math.Abs(x-med))
	}
	return median(devs)
}

// outlier scores how far one guess sat from the rest of the room, scaled by
// how spread out the room was.
//
// Scaled against the ROOM rather than against the correct answer on purpose.
// A bank that mixes "what year" with "how many feet" makes raw distance a
// measure of which question you blew, and relative error divides by zero the
// first time an answer is 0. This asks the question the award actually means:
// how far outside everyone else were you.
//
// A spread of zero is not a division to guard against and walk away from --
// it is the single best moment of the night, the whole room on 1969 and one
// table on four million. So it falls back to distance scaled by the median
// itself.
func outlier(value, med, mad float64) float64 {
	if mad > 0 {
		return math.Abs(value-med) / mad
	}
	return math.Abs(value-med) / math.Max(math.Abs(med), 1)
}

// countWord renders a count with its noun. "1 times" is the same bug as
// "You have 1 chips", which a one-chip game shipped once already; plural is
// core.go's suffix helper and every noun the pool counts is regular.
func countWord(n int, noun string) string {
	return fmt.Sprintf("%d %s%s", n, noun, plural(n))
}

// countAward is the shape most of the pool takes: count the rounds where
// something was true of a table, and describe the count.
//
// Tables that do not qualify, and counts of zero, are left out of the map
// entirely rather than scored 0 -- an award nobody earned should go
// unawarded, not to whoever sorts first.
func countAward(in AwardInput, q qualifier, detail func(n int) string,
	match func(r AwardRound, teamID uuid.UUID) bool,
) map[uuid.UUID]awardScore {
	out := map[uuid.UUID]awardScore{}
	for _, t := range in.Teams {
		if !q.OK(t.ID) {
			continue
		}
		n := 0
		for _, r := range in.Rounds {
			if q.Eligible(r, t.ID) && match(r, t.ID) {
				n++
			}
		}
		if n > 0 {
			out[t.ID] = awardScore{Value: float64(n), Detail: detail(n)}
		}
	}
	return out
}
