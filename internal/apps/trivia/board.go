package trivia

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"sort"
)

// BoardCandidate is a question the builder may place, reduced to what
// placement needs: an identity and the topics it can sit under.
type BoardCandidate struct {
	QuestionID string
	TopicKeys  []string
}

// Cell is one placed tile, in the order the board reads.
type Cell struct {
	ColIndex   int
	RowIndex   int
	Topic      string
	Points     int
	QuestionID string
}

// ShortfallError says which column could not be filled and by how much. The
// host is standing in a bar at 7pm; "no valid board" would send them hunting.
type ShortfallError struct {
	Topic  string
	Have   int
	Needed int
}

func (e *ShortfallError) Error() string {
	return fmt.Sprintf("topic %q has %d questions but the board needs %d", e.Topic, e.Have, e.Needed)
}

// BuildBoard assigns one question to every cell of a topics x rows grid.
//
// GREEDY FAILS ON REALISTIC BANKS, which is the whole reason this is a
// matching. A question carrying two topics can fill either column; filling a
// common topic first can consume the only questions a rare topic had, and the
// host gets a baffling "not enough questions" for a board that was perfectly
// fillable. Augmenting paths are about fifty lines, instant at this size, and
// they either produce a full board or prove that none exists.
//
// seed makes the choice deterministic, which is what lets the tests pin a
// specific bank to a specific board.
func BuildBoard(topics []string, rows int, values []int, bank []BoardCandidate, seed int64) ([]Cell, error) {
	if rows <= 0 || len(topics) == 0 {
		return nil, errors.New("board needs at least one topic and one row")
	}
	if len(values) < rows {
		return nil, fmt.Errorf("board has %d rows but only %d cell values", rows, len(values))
	}

	// One demand per (column, row). Each wants a question carrying that
	// column's topic.
	type demand struct{ col, row int }
	demands := make([]demand, 0, len(topics)*rows)
	for col := range topics {
		for row := range rows {
			demands = append(demands, demand{col, row})
		}
	}

	// Eligibility, shuffled per demand so a rerun with a new seed gives a
	// different board from the same bank rather than the same one forever.
	rng := rand.New(rand.NewSource(seed)) //nolint:gosec // board variety, not secrecy
	eligible := make([][]int, len(demands))
	for i, d := range demands {
		want := topics[d.col]
		var opts []int
		for qi, q := range bank {
			if slices.Contains(q.TopicKeys, want) {
				opts = append(opts, qi)
			}
		}
		// The bank arrives least-recently-used first, so a stable shuffle of
		// only the leading run would be ideal; shuffling wholesale and
		// letting the caller pre-truncate is simpler and the caller does
		// exactly that.
		rng.Shuffle(len(opts), func(a, b int) { opts[a], opts[b] = opts[b], opts[a] })
		eligible[i] = opts
	}

	// Fail fast with a message naming the column, before the matching runs,
	// for the common case of a topic that plainly has too few questions.
	if err := checkPerTopicSupply(topics, rows, bank); err != nil {
		return nil, err
	}

	assignedTo := make([]int, len(bank)) // question index -> demand index, -1 free
	for i := range assignedTo {
		assignedTo[i] = -1
	}
	for di := range demands {
		// The visited set is over QUESTIONS, not demands: Kuhn's algorithm
		// marks each right-hand vertex at most once per augmenting attempt,
		// and marking the left side instead quietly gives up on assignments
		// that were reachable.
		seen := make([]bool, len(bank))
		if !augment(di, eligible, assignedTo, seen) {
			d := demands[di]
			return nil, &ShortfallError{
				Topic:  topics[d.col],
				Have:   len(eligible[di]),
				Needed: rows,
			}
		}
	}

	cells := make([]Cell, 0, len(demands))
	for qi, di := range assignedTo {
		if di < 0 {
			continue
		}
		d := demands[di]
		cells = append(cells, Cell{
			ColIndex:   d.col,
			RowIndex:   d.row,
			Topic:      topics[d.col],
			Points:     values[d.row],
			QuestionID: bank[qi].QuestionID,
		})
	}
	sort.Slice(cells, func(a, b int) bool {
		if cells[a].RowIndex != cells[b].RowIndex {
			return cells[a].RowIndex < cells[b].RowIndex
		}
		return cells[a].ColIndex < cells[b].ColIndex
	})
	return cells, nil
}

// augment is the Kuhn's-algorithm step: try to seat demand di on one of its
// eligible questions, displacing whoever holds that question if the holder
// can move somewhere else. seenQ is per-attempt and indexed by question.
func augment(di int, eligible [][]int, assignedTo []int, seenQ []bool) bool {
	for _, qi := range eligible[di] {
		if seenQ[qi] {
			continue
		}
		seenQ[qi] = true
		holder := assignedTo[qi]
		if holder < 0 || augment(holder, eligible, assignedTo, seenQ) {
			assignedTo[qi] = di
			return true
		}
	}
	return false
}

// checkPerTopicSupply catches the obvious shortfall with a message naming the
// column and the gap. The matching would catch it too, but this reports the
// worst-supplied topic rather than whichever demand happened to fail first,
// which is what the host needs to go fix.
func checkPerTopicSupply(topics []string, rows int, bank []BoardCandidate) error {
	counts := map[string]int{}
	for _, q := range bank {
		for _, tk := range q.TopicKeys {
			counts[tk]++
		}
	}
	var worst *ShortfallError
	for _, t := range topics {
		if have := counts[t]; have < rows {
			if worst == nil || have < worst.Have {
				worst = &ShortfallError{Topic: t, Have: have, Needed: rows}
			}
		}
	}
	if worst != nil {
		return worst
	}
	return nil
}

// PickTopics defaults the board's columns to the topics with the most unused
// questions, which is the choice a host would make by hand.
//
// It is only a default: the column set is a host decision made in setup,
// because "Sports" and "Sportsball" arriving from a CSV as two topics is a
// real thing and the host has to see it and fix it. The Auto button rerolls
// among the viable ones.
func PickTopics(hist []TopicCount, columns, rows int, seed int64) []string {
	viable := make([]TopicCount, 0, len(hist))
	for _, tc := range hist {
		if tc.Total >= rows {
			viable = append(viable, tc)
		}
	}
	sort.Slice(viable, func(a, b int) bool {
		if viable[a].Unused != viable[b].Unused {
			return viable[a].Unused > viable[b].Unused
		}
		if viable[a].Total != viable[b].Total {
			return viable[a].Total > viable[b].Total
		}
		return viable[a].Key < viable[b].Key
	})
	if seed != 0 && len(viable) > columns {
		rng := rand.New(rand.NewSource(seed)) //nolint:gosec // variety, not secrecy
		rng.Shuffle(len(viable), func(a, b int) { viable[a], viable[b] = viable[b], viable[a] })
	}
	out := make([]string, 0, columns)
	for i := 0; i < len(viable) && i < columns; i++ {
		out = append(out, viable[i].Key)
	}
	return out
}
