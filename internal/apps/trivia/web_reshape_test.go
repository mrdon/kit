package trivia

import (
	"net/http"
	"testing"
)

// A board is a rendering of the shape settings. Change the shape and the old
// board must not survive: the host typed 2 and the TV kept showing 5.
func TestChangingTheShapeRedrawsTheBoard(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	s := defaultSettings()
	s.RepeatQuestions = true
	game := f.newGame(s, topicSet())
	before, err := ListBoardCells(f.ctx, f.pool, f.tenant.ID, game.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != s.BoardRows*s.BoardColumns {
		t.Fatalf("fixture board has %d cells, want %d", len(before), s.BoardRows*s.BoardColumns)
	}

	s.BoardColumns = 2
	status, body := f.serveConsole(t, http.MethodPatch,
		"/"+f.tenant.Slug+"/api/trivia/games/"+game.ID.String(),
		map[string]any{"settings": s})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", status, body)
	}
	after, err := ListBoardCells(f.ctx, f.pool, f.tenant.ID, game.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 2*s.BoardRows {
		t.Fatalf("board has %d cells after narrowing to 2 columns, want %d", len(after), 2*s.BoardRows)
	}
	for _, c := range after {
		if c.ColIndex >= 2 {
			t.Fatalf("a cell sits in column %d of a 2-column board", c.ColIndex)
		}
	}
}

// A timer change is not a shape change, and must leave the board alone: the
// host who tweaks the betting clock did not ask for new categories.
func TestChangingATimerKeepsTheBoard(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	s := defaultSettings()
	game := f.newGame(s, topicSet())
	before, _ := ListBoardCells(f.ctx, f.pool, f.tenant.ID, game.ID)

	s.BetSeconds = 90
	status, body := f.serveConsole(t, http.MethodPatch,
		"/"+f.tenant.Slug+"/api/trivia/games/"+game.ID.String(),
		map[string]any{"settings": s})
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", status, body)
	}
	after, _ := ListBoardCells(f.ctx, f.pool, f.tenant.ID, game.ID)
	if len(after) != len(before) || after[0].ID != before[0].ID {
		t.Fatal("a timer change redrew the board")
	}
}
