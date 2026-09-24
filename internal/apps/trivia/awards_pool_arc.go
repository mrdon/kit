package trivia

import (
	"fmt"

	"github.com/google/uuid"
)

// The awards that need the shape of the whole night rather than one round:
// where a table stood halfway through, what it did with the final, and whose
// chips it kept following.

// minFollowed is how many chips have to land on one other table before
// following them is a habit rather than a coincidence.
const minFollowed = 3

// evalKingmaker finds the table that spent the night backing one other
// table's answers.
//
// A chip on a shared card counts for every table that wrote it, which is the
// honest reading: you did back them, whoever else was on it.
func evalKingmaker(in AwardInput, q qualifier) map[uuid.UUID]awardScore {
	names := teamNames(in)
	out := map[uuid.UUID]awardScore{}
	for _, t := range in.Teams {
		if !q.OK(t.ID) {
			continue
		}
		followed := map[uuid.UUID]int{}
		for _, r := range in.Rounds {
			if !q.Eligible(r, t.ID) {
				continue
			}
			for _, b := range betsOf(r, t.ID) {
				creditFollowers(r, b, t.ID, followed)
			}
		}
		// Walk in.Teams, not the map: same tie-breaking reason as resolve.
		var bestID uuid.UUID
		best := 0
		for _, o := range in.Teams {
			if followed[o.ID] > best {
				bestID, best = o.ID, followed[o.ID]
			}
		}
		if best >= minFollowed {
			out[t.ID] = awardScore{
				Value:  float64(best),
				Detail: fmt.Sprintf("Put %s on %s.", countWord(best, "chip"), names[bestID]),
			}
		}
	}
	return out
}

// creditFollowers adds one chip to every OTHER table that wrote the card it
// landed on.
func creditFollowers(r AwardRound, b AwardBet, teamID uuid.UUID, followed map[uuid.UUID]int) {
	for _, s := range r.Slots {
		if s.ID != b.SlotID {
			continue
		}
		for _, id := range s.TeamIDs {
			if id != teamID {
				followed[id]++
			}
		}
	}
}

func teamNames(in AwardInput) map[uuid.UUID]string {
	out := map[uuid.UUID]string{}
	for _, t := range in.Teams {
		out[t.ID] = t.Name
	}
	return out
}

// evalBiggestRound finds the single best round anybody had.
func evalBiggestRound(in AwardInput, q qualifier) map[uuid.UUID]awardScore {
	out := map[uuid.UUID]awardScore{}
	for _, t := range in.Teams {
		if !q.OK(t.ID) {
			continue
		}
		best, where := 0, ""
		for _, r := range in.Rounds {
			d, ok := r.Deltas[t.ID]
			if !ok || d.Total() <= best {
				continue
			}
			best, where = d.Total(), roundWord(r)
			out[t.ID] = awardScore{
				Value:  float64(best),
				Detail: fmt.Sprintf("Took %s on %s.", FormatMoney(best), where),
			}
		}
	}
	return out
}

// roundWord names a round the way the room would.
func roundWord(r AwardRound) string {
	if r.IsFinal {
		return "the final"
	}
	return fmt.Sprintf("question %d", r.Ordinal+1)
}

// evalComeback finds the biggest climb from halfway through the night.
//
// It structurally rewards a bad start, which is why it sits behind the
// temperament awards in the selection order rather than in front of them.
func evalComeback(in AwardInput, q qualifier) map[uuid.UUID]awardScore {
	if len(in.Rounds) < 4 {
		return nil
	}
	mid := len(in.Rounds) / 2
	midRanks := ranksAfter(in, mid)
	endRanks := ranksAfter(in, len(in.Rounds))
	out := map[uuid.UUID]awardScore{}
	for _, t := range in.Teams {
		if !q.OK(t.ID) || !q.Eligible(in.Rounds[0], t.ID) {
			continue
		}
		climb := midRanks[t.ID] - endRanks[t.ID]
		if climb <= 0 {
			continue
		}
		out[t.ID] = awardScore{
			Value: float64(climb),
			Detail: fmt.Sprintf("Climbed from %s to %s.",
				ordinalWord(midRanks[t.ID]), ordinalWord(endRanks[t.ID])),
		}
	}
	return out
}

// ranksAfter is every table's competition rank once n rounds have been
// scored. Two tables on the same money share a rank, the same rule the
// podium uses for its crown.
func ranksAfter(in AwardInput, n int) map[uuid.UUID]int {
	totals := map[uuid.UUID]int{}
	for i, r := range in.Rounds {
		if i >= n {
			break
		}
		for id, d := range r.Deltas {
			totals[id] += d.Total()
		}
	}
	out := map[uuid.UUID]int{}
	for _, t := range in.Teams {
		rank := 1
		for _, o := range in.Teams {
			if totals[o.ID] > totals[t.ID] {
				rank++
			}
		}
		out[t.ID] = rank
	}
	return out
}

func ordinalWord(n int) string {
	if n%100 >= 11 && n%100 <= 13 {
		return fmt.Sprintf("%dth", n)
	}
	switch n % 10 {
	case 1:
		return fmt.Sprintf("%dst", n)
	case 2:
		return fmt.Sprintf("%dnd", n)
	case 3:
		return fmt.Sprintf("%drd", n)
	}
	return fmt.Sprintf("%dth", n)
}

// evalWentForIt finds the table that put the most of its bank on the final.
func evalWentForIt(in AwardInput, q qualifier) map[uuid.UUID]awardScore {
	return finalWagerShare(in, q)
}

// evalPlayedSafe is the same measurement picked from the other end, so the
// two can never be the same table.
func evalPlayedSafe(in AwardInput, q qualifier) map[uuid.UUID]awardScore {
	return finalWagerShare(in, q)
}

// finalWagerShare is what each table staked on the final as a fraction of
// what it had going in.
//
// A fraction rather than an amount, because the leader can wager more than
// anybody else while risking less of itself, and nerve is what the award is
// about. Read off the final round's chip rather than the wager row: the
// amount is the same and it saves the query.
func finalWagerShare(in AwardInput, q qualifier) map[uuid.UUID]awardScore {
	final, bank := -1, map[uuid.UUID]int{}
	for i, r := range in.Rounds {
		if r.IsFinal {
			final = i
			break
		}
		for id, d := range r.Deltas {
			bank[id] += d.Total()
		}
	}
	if final < 0 {
		return nil
	}
	out := map[uuid.UUID]awardScore{}
	for _, t := range in.Teams {
		staked := 0
		for _, b := range betsOf(in.Rounds[final], t.ID) {
			staked += b.Amount
		}
		if !q.OK(t.ID) || staked <= 0 || bank[t.ID] <= 0 {
			continue
		}
		out[t.ID] = awardScore{
			Value: float64(staked) / float64(bank[t.ID]),
			Detail: fmt.Sprintf("Wagered %s of %s on the final.",
				FormatMoney(staked), FormatMoney(bank[t.ID])),
		}
	}
	return out
}
