package trivia

import (
	"errors"
	"fmt"
	"slices"
	"testing"
)

func bank(entries ...[]string) []BoardCandidate {
	out := make([]BoardCandidate, 0, len(entries))
	for i, topics := range entries {
		out = append(out, BoardCandidate{QuestionID: fmt.Sprintf("q%d", i), TopicKeys: topics})
	}
	return out
}

// The default board, end to end: every cell filled, no question used twice.
func TestBuildBoardFillsEveryCellExactlyOnce(t *testing.T) {
	topics := []string{"space", "sports", "film", "food", "history"}
	var entries [][]string
	for _, tp := range topics {
		for range 4 {
			entries = append(entries, []string{tp})
		}
	}
	cells, err := BuildBoard(topics, 2, []int{500, 1000}, bank(entries...), 7)
	if err != nil {
		t.Fatalf("BuildBoard: %v", err)
	}
	if len(cells) != 10 {
		t.Fatalf("got %d cells, want 10 (5 columns x 2 rows)", len(cells))
	}
	seenQ := map[string]bool{}
	seenPos := map[[2]int]bool{}
	for _, c := range cells {
		if seenQ[c.QuestionID] {
			t.Fatalf("question %s appears on the board twice", c.QuestionID)
		}
		seenQ[c.QuestionID] = true
		pos := [2]int{c.ColIndex, c.RowIndex}
		if seenPos[pos] {
			t.Fatalf("two questions at column %d row %d", c.ColIndex, c.RowIndex)
		}
		seenPos[pos] = true
		if c.Topic != topics[c.ColIndex] {
			t.Fatalf("cell at column %d has topic %q, want %q", c.ColIndex, c.Topic, topics[c.ColIndex])
		}
	}
	// Cell values follow the row, cheapest first.
	for _, c := range cells {
		want := []int{500, 1000}[c.RowIndex]
		if c.Points != want {
			t.Fatalf("row %d cell is worth %d, want %d", c.RowIndex, c.Points, want)
		}
	}
}

// THE REGRESSION THAT JUSTIFIES THE ALGORITHM.
//
// "space" has exactly two questions and both also carry "film". "film" has
// three more of its own. A greedy pass that fills film first can consume both
// shared questions and then report that space is short — for a board that a
// matching fills without difficulty. This is not a contrived shape: a
// question about 2001: A Space Odyssey genuinely belongs to both.
func TestBuildBoardSucceedsWhereGreedyWouldFail(t *testing.T) {
	topics := []string{"film", "space"}
	b := bank(
		[]string{"film", "space"}, // the two shared questions greedy would eat
		[]string{"film", "space"},
		[]string{"film"},
		[]string{"film"},
	)
	// First, prove the bank really is a discriminator: a greedy pass that
	// takes the first eligible question for each cell in order strands
	// "space" on it. If this ever stops failing, the test below has stopped
	// testing anything.
	if greedyFills(topics, 2, b) {
		t.Fatal("the fixture no longer defeats greedy — the regression it guards is untested")
	}

	for seed := range int64(20) {
		cells, err := BuildBoard(topics, 2, []int{500, 1000}, b, seed)
		if err != nil {
			t.Fatalf("seed %d: BuildBoard failed on a bank with a valid assignment: %v", seed, err)
		}
		if len(cells) != 4 {
			t.Fatalf("seed %d: got %d cells, want 4", seed, len(cells))
		}
		byTopic := map[string]int{}
		for _, c := range cells {
			byTopic[c.Topic]++
		}
		if byTopic["space"] != 2 || byTopic["film"] != 2 {
			t.Fatalf("seed %d: columns filled %v, want two each", seed, byTopic)
		}
	}
}

// greedyFills is the algorithm this package deliberately does not use: walk
// the cells in order and take the first unused question carrying the right
// topic. Kept in the test as the thing being ruled out.
func greedyFills(topics []string, rows int, b []BoardCandidate) bool {
	used := map[int]bool{}
	for _, topic := range topics {
		filled := 0
		for qi, q := range b {
			if used[qi] {
				continue
			}
			if slices.Contains(q.TopicKeys, topic) {
				used[qi] = true
				filled++
			}
			if filled == rows {
				break
			}
		}
		if filled < rows {
			return false
		}
	}
	return true
}

// A genuinely under-supplied topic has to say so by name, with the shortfall.
// "No valid board" at 7pm sends the host hunting.
func TestBuildBoardNamesTheUnderSuppliedTopic(t *testing.T) {
	topics := []string{"space", "food"}
	b := bank(
		[]string{"space"}, []string{"space"}, []string{"space"},
		[]string{"food"}, // only one
	)
	_, err := BuildBoard(topics, 3, []int{100, 200, 300}, b, 1)
	if err == nil {
		t.Fatal("expected a shortfall error")
	}
	var se *ShortfallError
	if !errors.As(err, &se) {
		t.Fatalf("error %v is not a *ShortfallError", err)
	}
	if se.Topic != "food" || se.Have != 1 || se.Needed != 3 {
		t.Fatalf("shortfall = %+v, want food 1/3", se)
	}
	if got := se.Error(); got != `topic "food" has 1 questions but the board needs 3` {
		t.Fatalf("message = %q", got)
	}
}

// The same seed builds the same board, which is what lets a host reroll with
// Auto and get something genuinely different rather than the same grid.
func TestBuildBoardIsDeterministicPerSeed(t *testing.T) {
	topics := []string{"a", "b"}
	var entries [][]string
	for _, tp := range topics {
		for range 6 {
			entries = append(entries, []string{tp})
		}
	}
	b := bank(entries...)
	first, err := BuildBoard(topics, 2, []int{500, 1000}, b, 42)
	if err != nil {
		t.Fatal(err)
	}
	again, err := BuildBoard(topics, 2, []int{500, 1000}, b, 42)
	if err != nil {
		t.Fatal(err)
	}
	for i := range first {
		if first[i] != again[i] {
			t.Fatalf("same seed produced different boards at %d: %+v vs %+v", i, first[i], again[i])
		}
	}
}

// The setup page's default columns: most unused questions first, and never a
// topic that cannot fill its column.
func TestPickTopicsPrefersUnusedAndSkipsThinTopics(t *testing.T) {
	hist := []TopicCount{
		{Key: "space", Total: 8, Unused: 8},
		{Key: "film", Total: 9, Unused: 2},
		{Key: "thin", Total: 1, Unused: 1},
		{Key: "food", Total: 5, Unused: 5},
	}
	got := PickTopics(hist, 2, 2, 0)
	if len(got) != 2 {
		t.Fatalf("got %d topics, want 2", len(got))
	}
	for _, k := range got {
		if k == "thin" {
			t.Fatal("picked a topic with fewer questions than the board has rows")
		}
	}
	if got[0] != "space" {
		t.Fatalf("first pick = %q, want the topic with the most unused questions", got[0])
	}
}

// Not enough cell values for the requested rows is a caller bug, and it has
// to be caught before a board is half-built.
func TestBuildBoardRejectsTooFewCellValues(t *testing.T) {
	_, err := BuildBoard([]string{"a"}, 3, []int{500}, bank([]string{"a"}), 1)
	if err == nil {
		t.Fatal("expected an error for 3 rows and 1 cell value")
	}
}
