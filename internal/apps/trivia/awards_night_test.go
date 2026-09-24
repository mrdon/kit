package trivia

import "testing"

// A realistic five-table night, end to end: five awards, five different
// tables, and the card that builds to the podium is the one worth building
// to. This is the test that would have caught "wildest guesses" being beaten
// to its own winner by a duller award and never reaching the wall.
func TestARealisticNightFillsEverySlot(t *testing.T) {
	g := newAwardGame(t, "Bar Flies", "Quiz Khalifa", "Norwegian Wood", "The Quizzards", "Table 9")
	g.round(1969, map[string]float64{"Bar Flies": 1969, "Quiz Khalifa": 4000000, "Norwegian Wood": 1970, "The Quizzards": 1965, "Table 9": 1969}).
		bet("Bar Flies", "Bar Flies", 100).bet("Quiz Khalifa", "Bar Flies", 200).
		bet("Norwegian Wood", "Bar Flies", 100).bet("The Quizzards", "The Quizzards", 200).
		bet("Table 9", "Bar Flies", 100).
		delta("Bar Flies", 100, 100).delta("Quiz Khalifa", 0, 200).delta("Norwegian Wood", 0, 100).
		delta("The Quizzards", 0, 0).delta("Table 9", 100, 100)
	g.round(12, map[string]float64{"Bar Flies": 11, "Quiz Khalifa": 4000000, "Norwegian Wood": 13, "The Quizzards": 10, "Table 9": 12}).
		bet("Bar Flies", "Bar Flies", 100).bet("Quiz Khalifa", "Table 9", 200).
		bet("Norwegian Wood", "Bar Flies", 100).bet("The Quizzards", "The Quizzards", 100).
		bet("Table 9", "Bar Flies", 200).
		delta("Bar Flies", 0, 0).delta("Quiz Khalifa", 0, 200).delta("Norwegian Wood", 0, 0).
		delta("The Quizzards", 0, 0).delta("Table 9", 100, 0)
	g.round(500, map[string]float64{"Bar Flies": 450, "Quiz Khalifa": 900, "Norwegian Wood": 510, "The Quizzards": 400, "Table 9": 499}).
		bet("Bar Flies", "Bar Flies", 100).bet("Quiz Khalifa", "Table 9", 200).
		bet("Norwegian Wood", "Table 9", 100).bet("The Quizzards", "The Quizzards", 100).
		bet("Table 9", "Table 9", 200).
		delta("Bar Flies", 0, 0).delta("Quiz Khalifa", 0, 200).delta("Norwegian Wood", 0, 100).
		delta("The Quizzards", 0, 0).delta("Table 9", 100, 200)
	g.round(75, map[string]float64{"Bar Flies": 70, "Quiz Khalifa": 60000, "Norwegian Wood": 76, "The Quizzards": 65, "Table 9": 74}).
		bet("Bar Flies", "Bar Flies", 100).bet("Quiz Khalifa", "Table 9", 200).
		bet("Norwegian Wood", "Table 9", 100).bet("The Quizzards", "The Quizzards", 100).
		bet("Table 9", "Bar Flies", 200).
		delta("Bar Flies", 0, 0).delta("Quiz Khalifa", 0, 200).delta("Norwegian Wood", 0, 100).
		delta("The Quizzards", 0, 0).delta("Table 9", 100, 0)
	g.round(30, map[string]float64{"Bar Flies": 28, "Quiz Khalifa": 31, "Norwegian Wood": 29, "The Quizzards": 25, "Table 9": 30}).
		final().bet("Bar Flies", "Table 9", 300).bet("Quiz Khalifa", "Table 9", 700).
		bet("Norwegian Wood", "Table 9", 100).bet("The Quizzards", "The Quizzards", 50).
		bet("Table 9", "Table 9", 200).
		delta("Bar Flies", 0, 300).delta("Quiz Khalifa", 0, 700).delta("Norwegian Wood", 0, 100).
		delta("The Quizzards", 0, -50).delta("Table 9", 100, 200)

	got := Awards(g.in)
	for i, a := range got {
		t.Logf("%d. %-22s %-16s %s", i+1, a.Title, a.TeamName, a.Detail)
	}
	if len(got) != MaxAwards {
		t.Fatalf("got %d awards for a five-table night, want %d", len(got), MaxAwards)
	}
	seen := map[string]bool{}
	for _, a := range got {
		if seen[a.TeamName] {
			t.Errorf("%s holds two awards", a.TeamName)
		}
		seen[a.TeamName] = true
	}
	last := got[len(got)-1]
	if last.Key != "wildest" {
		t.Errorf("the last card before the podium is %q, want wildest guesses", last.Key)
	}
	if last.TeamName != "Quiz Khalifa" {
		t.Errorf("wildest guesses went to %s, want Quiz Khalifa, who answered 4,000,000 twice", last.TeamName)
	}
	if want := "The answer was 12. They said 4,000,000."; last.Detail != want {
		t.Errorf("detail = %q, want %q", last.Detail, want)
	}
}
