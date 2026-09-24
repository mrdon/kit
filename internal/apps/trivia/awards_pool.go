package trivia

import (
	"fmt"
	"math"

	"github.com/google/uuid"
)

// The pool. Slice order is SELECTION priority, Display is where a chosen
// award sits on the wall.
//
// Priority leads with the awards that are least correlated with winning,
// because those are the ones most likely to find a table the podium will not
// already be thanking. Display runs gentle to funny: the sympathetic ones
// early, and the last card before the crown is the one the bar makes a noise
// at.
//
// Titles are sentence case here and may be set however the surface likes;
// uppercase on the TV is a CSS decision, not a copy one. Every Detail is a
// straight statement of the fact, because the title is already carrying
// whatever joke the award has. See the writing-copy skill's humour budget:
// ship one joke where you found five, and deliver the straight lines
// straight.
var awardPool = []awardDef{
	// Wildest goes first because it produces the best single card in the
	// pool ("The answer was 12. They said 4,000,000.") and the table that
	// earns it usually earns two or three of the others as well. Left
	// further down, it was reliably beaten to its own winner by a duller
	// award and never made the wall.
	{Key: "wildest", Title: "Wildest guesses", Higher: true, Display: 90, Eval: evalWildest},
	{Key: "backed_themselves", Title: "Backed themselves", Higher: true, Display: 40, Eval: evalBackedThemselves},
	{Key: "trusted_room", Title: "Trusted the room", Higher: true, Display: 41, Eval: evalTrustedRoom},
	{Key: "so_close", Title: "So close all night", Higher: true, Display: 10, Eval: evalSoClose},
	{Key: "doubted", Title: "Doubted themselves", Higher: true, Display: 80, Eval: evalDoubted},
	{Key: "kingmaker", Title: "Followed the leader", Higher: true, Display: 50, Eval: evalKingmaker},
	{Key: "fastest", Title: "Fastest fingers", Higher: true, Display: 30, Eval: evalFastest},
	{Key: "last_word", Title: "Last to decide", Higher: true, Display: 31, Eval: evalLastWord},
	{Key: "all_in", Title: "All in", Higher: true, Display: 60, Eval: evalAllIn},
	{Key: "hedged", Title: "Hedged every time", Higher: true, Display: 61, Eval: evalHedged},
	{Key: "went_for_it", Title: "Went for it", Higher: true, Display: 70, Eval: evalWentForIt},
	{Key: "played_safe", Title: "Played it safe", Higher: false, Display: 71, Eval: evalPlayedSafe},
	{Key: "comeback", Title: "Best comeback", Higher: true, Display: 20, Eval: evalComeback},
	{Key: "biggest_round", Title: "Biggest round", Higher: true, Display: 21, Eval: evalBiggestRound},
	{Key: "dead_on", Title: "Dead on", Higher: true, Display: 11, Eval: evalDeadOn},
	{Key: "most_correct", Title: "Most answers right", Higher: true, Display: 12, Eval: evalMostCorrect},
	{Key: "never_passed", Title: "Never passed", Higher: true, Display: 13, Eval: evalNeverPassed},
}

// evalMostCorrect counts the rounds a table wrote the winning card. This is
// the board channel, which under flat $100 cells is the minority of a score
// -- betting pays more -- so it is genuinely not the same statistic as the
// podium's.
func evalMostCorrect(in AwardInput, q qualifier) map[uuid.UUID]awardScore {
	return countAward(in, q,
		func(n int) string {
			return fmt.Sprintf("Wrote the winning answer %s.", countWord(n, "time"))
		},
		func(r AwardRound, id uuid.UUID) bool { return wroteWinner(r, id) })
}

// evalDeadOn counts answers that were exactly right, which the scoring rule
// never pays extra for.
func evalDeadOn(in AwardInput, q qualifier) map[uuid.UUID]awardScore {
	return countAward(in, q,
		func(n int) string { return fmt.Sprintf("Exactly right %s.", countWord(n, "time")) },
		func(r AwardRound, id uuid.UUID) bool {
			a, ok := answerOf(r, id)
			return ok && a.Value == r.Correct
		})
}

// evalSoClose counts the rounds a table held the nearest guess that was
// still over.
//
// Closest without going over means these tables were the best informed in
// the room and were paid nothing for it, every time. This is the one award
// that exists to say so.
func evalSoClose(in AwardInput, q qualifier) map[uuid.UUID]awardScore {
	return countAward(in, q,
		func(n int) string {
			return fmt.Sprintf("Held the nearest guess that was over, %s.", countWord(n, "time"))
		},
		func(r AwardRound, id uuid.UUID) bool {
			a, ok := answerOf(r, id)
			if !ok || a.Value <= r.Correct {
				return false
			}
			for _, o := range r.Answers {
				if o.Value > r.Correct && o.Value < a.Value {
					return false
				}
			}
			return true
		})
}

// evalDoubted counts the rounds a table wrote the winning card and put no
// chip on it.
func evalDoubted(in AwardInput, q qualifier) map[uuid.UUID]awardScore {
	return countAward(in, q,
		func(n int) string {
			return fmt.Sprintf("Wrote the winning answer %s and bet elsewhere.", countWord(n, "time"))
		},
		func(r AwardRound, id uuid.UUID) bool {
			if !wroteWinner(r, id) {
				return false
			}
			for _, b := range betsOf(r, id) {
				if b.SlotID == r.WinningSlotID {
					return false
				}
			}
			return true
		})
}

// evalNeverPassed finds the tables that answered every round they were in.
// Scored by how many rounds that was, so a table there from the first
// question beats one that arrived at the fourth.
func evalNeverPassed(in AwardInput, q qualifier) map[uuid.UUID]awardScore {
	out := map[uuid.UUID]awardScore{}
	for _, t := range in.Teams {
		if !q.OK(t.ID) {
			continue
		}
		played, answered := 0, 0
		for _, r := range in.Rounds {
			if !q.Eligible(r, t.ID) {
				continue
			}
			played++
			if _, ok := answerOf(r, t.ID); ok {
				answered++
			}
		}
		if played > 0 && played == answered {
			out[t.ID] = awardScore{
				Value:  float64(played),
				Detail: fmt.Sprintf("Answered all %s.", countWord(played, "question")),
			}
		}
	}
	return out
}

// evalFastest counts the rounds a table was first to commit an answer.
func evalFastest(in AwardInput, q qualifier) map[uuid.UUID]awardScore {
	return countAward(in, q,
		func(n int) string { return fmt.Sprintf("First to answer %s.", countWord(n, "time")) },
		func(r AwardRound, id uuid.UUID) bool { return extremeAnswerer(r, id, true) })
}

// evalLastWord counts the rounds a table was last in.
func evalLastWord(in AwardInput, q qualifier) map[uuid.UUID]awardScore {
	return countAward(in, q,
		func(n int) string { return fmt.Sprintf("Last to answer %s.", countWord(n, "time")) },
		func(r AwardRound, id uuid.UUID) bool { return extremeAnswerer(r, id, false) })
}

// extremeAnswerer reports whether a table was first (or last) to submit in a
// round. A round with one answer in it has no race to win.
func extremeAnswerer(r AwardRound, teamID uuid.UUID, first bool) bool {
	a, ok := answerOf(r, teamID)
	if !ok || len(r.Answers) < 2 {
		return false
	}
	for _, o := range r.Answers {
		if o.TeamID == teamID {
			continue
		}
		if first && o.SubmittedAt.Before(a.SubmittedAt) {
			return false
		}
		if !first && o.SubmittedAt.After(a.SubmittedAt) {
			return false
		}
	}
	return true
}

// evalBackedThemselves is the share of a table's chips that landed on a card
// it had written.
func evalBackedThemselves(in AwardInput, q qualifier) map[uuid.UUID]awardScore {
	return chipShare(in, q, true, func(own, total int) string {
		return fmt.Sprintf("Put %d of %s on their own answer.", own, countWord(total, "chip"))
	})
}

// evalTrustedRoom is the mirror: chips on somebody else's card.
//
// A chip on the pseudo-slot counts here. Nobody wrote that card, so backing
// it is the purest form of not backing yourself.
func evalTrustedRoom(in AwardInput, q qualifier) map[uuid.UUID]awardScore {
	return chipShare(in, q, false, func(other, total int) string {
		return fmt.Sprintf("Put %d of %s on other tables.", other, countWord(total, "chip"))
	})
}

// chipShare counts chips on (or off) a table's own card as a fraction of the
// chips it placed.
//
// These two awards are opposite ends of ONE axis -- the shares sum to one --
// so their winners are guaranteed to be different tables. That is the
// strongest distinctness property in the pool, and the reason both are near
// the front of the selection order.
//
// Rounds where a table wrote nothing are skipped: with no card of its own on
// the mat, every chip it places is somebody else's by force, and counting
// those would hand "trusted the room" to whoever answered least.
func chipShare(in AwardInput, q qualifier, own bool, detail func(n, total int) string) map[uuid.UUID]awardScore {
	out := map[uuid.UUID]awardScore{}
	for _, t := range in.Teams {
		if !q.OK(t.ID) {
			continue
		}
		hit, total := 0, 0
		for _, r := range in.Rounds {
			if !q.Eligible(r, t.ID) {
				continue
			}
			if _, ok := slotOf(r, t.ID); !ok {
				continue
			}
			for _, b := range betsOf(r, t.ID) {
				total++
				if onOwnCard(r, b, t.ID) == own {
					hit++
				}
			}
		}
		if total > 0 {
			out[t.ID] = awardScore{Value: float64(hit) / float64(total), Detail: detail(hit, total)}
		}
	}
	return out
}

// evalAllIn counts the rounds a table put every chip it had on one card.
func evalAllIn(in AwardInput, q qualifier) map[uuid.UUID]awardScore {
	return countAward(in, q,
		func(n int) string { return fmt.Sprintf("Stacked every chip %s.", countWord(n, "time")) },
		func(r AwardRound, id uuid.UUID) bool { return chipSpread(r, id) == 1 })
}

// evalHedged counts the rounds a table spread its chips across cards.
func evalHedged(in AwardInput, q qualifier) map[uuid.UUID]awardScore {
	return countAward(in, q,
		func(n int) string { return fmt.Sprintf("Split their chips %s.", countWord(n, "time")) },
		func(r AwardRound, id uuid.UUID) bool { return chipSpread(r, id) > 1 })
}

// chipSpread is how many different cards a table's chips sat on, and 0 when
// it had fewer than two chips down -- a single chip is neither a stack nor a
// hedge, and a final only ever deals one.
func chipSpread(r AwardRound, teamID uuid.UUID) int {
	bets := betsOf(r, teamID)
	if len(bets) < 2 {
		return 0
	}
	seen := map[uuid.UUID]bool{}
	for _, b := range bets {
		seen[b.SlotID] = true
	}
	return len(seen)
}

// evalWildest finds the table whose numbers sat furthest outside the room's.
//
// Averaged over the night so it describes how a table plays rather than
// which question it blew, but the line underneath quotes the single wildest
// moment, because that is the one the bar reacts to.
func evalWildest(in AwardInput, q qualifier) map[uuid.UUID]awardScore {
	out := map[uuid.UUID]awardScore{}
	for _, t := range in.Teams {
		if !q.OK(t.ID) {
			continue
		}
		sum, n, worst := 0.0, 0, ""
		high := math.Inf(-1)
		for _, r := range in.Rounds {
			g := guesses(r)
			// Under three numbers there is no room to be outside of.
			if !q.Eligible(r, t.ID) || len(g) < 3 {
				continue
			}
			a, ok := answerOf(r, t.ID)
			if !ok {
				continue
			}
			med := median(g)
			score := outlier(a.Value, med, medianAbsDev(g, med))
			sum, n = sum+score, n+1
			if score > high {
				high, worst = score, fmt.Sprintf("The answer was %s. They said %s.",
					answerTextOf(r), FormatValue(a.Value))
			}
		}
		if n > 0 && high > 0 {
			out[t.ID] = awardScore{Value: sum / float64(n), Detail: worst}
		}
	}
	return out
}

// answerTextOf prefers the spelling a human wrote ("$1,200") over %g output.
func answerTextOf(r AwardRound) string {
	if r.CorrectText != "" {
		return r.CorrectText
	}
	return FormatValue(r.Correct)
}
