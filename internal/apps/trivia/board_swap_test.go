package trivia

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// decodeCells pulls the setup page's board out of a console response.
func decodeCells(t *testing.T, body string) []cellJSON {
	t.Helper()
	var resp struct {
		Cells []cellJSON `json:"cells"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("decoding cells: %v (body %q)", err, body)
	}
	return resp.Cells
}

// consolePath is the host API's address for a game, the public gamePath's
// counterpart.
func (f *fixture) consolePath(gameID uuid.UUID) string {
	return "/" + f.tenant.Slug + "/api/trivia/games/" + gameID.String()
}

// The host cannot check a board they cannot read. Every cell has to come back
// with the question behind it and the answer it expects, or "build the board"
// is still a leap of faith.
func TestSetupBoardCarriesItsQuestions(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())

	status, body := f.serveConsole(t, http.MethodGet, f.consolePath(game.ID), nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", status, body)
	}
	cells := decodeCells(t, body)
	if len(cells) != 10 {
		t.Fatalf("got %d cells, want the whole 5x2 board", len(cells))
	}
	for _, c := range cells {
		if c.Prompt == "" || c.Answer == "" {
			t.Fatalf("cell %s came back without a question: %+v", c.ID, c)
		}
		// Four per topic, two of them on the board.
		if c.Spares != 2 {
			t.Fatalf("cell in %q reports %d spare, want 2", c.Topic, c.Spares)
		}
	}
}

// The whole point: one tile changes and the other nine do not. A host who
// dislikes one question should not have to reroll a board they were happy
// with.
func TestSwapChangesOneCellAndLeavesTheRest(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())

	_, body := f.serveConsole(t, http.MethodGet, f.consolePath(game.ID), nil)
	before := decodeCells(t, body)
	target := before[0]

	status, body := f.serveConsole(t, http.MethodPost,
		f.consolePath(game.ID)+"/board/cells/"+target.ID+"/swap", nil)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", status, body)
	}
	after := decodeCells(t, body)
	if len(after) != len(before) {
		t.Fatalf("the board went from %d cells to %d", len(before), len(after))
	}
	byID := map[string]cellJSON{}
	for _, c := range after {
		byID[c.ID] = c
	}
	got := byID[target.ID]
	if got.Prompt == target.Prompt {
		t.Fatal("the swapped cell is still asking the same question")
	}
	// The column and the money must not move: nothing the room can see has
	// changed, which is what makes this safe to press.
	if got.Topic != target.Topic || got.Points != target.Points || got.Col != target.Col || got.Row != target.Row {
		t.Fatalf("the swap moved the tile: %+v was %+v", got, target)
	}
	// The count holds: the tile gives its old question back to the category
	// as it takes a new one, so rotating does not eat the bank and the button
	// does not grey itself out after a few presses.
	if got.Spares != target.Spares {
		t.Fatalf("spares went %d -> %d; a swap releases as many as it takes",
			target.Spares, got.Spares)
	}
	for _, c := range before {
		if c.ID == target.ID {
			continue
		}
		if byID[c.ID].Prompt != c.Prompt {
			t.Fatalf("swapping one cell rewrote another (%s, %q)", c.Topic, c.Prompt)
		}
	}
}

// Pressing it again has to reach past what it just dealt, or the button is a
// toggle between two questions rather than a way through the category.
func TestSwapRotatesRatherThanToggles(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())

	_, body := f.serveConsole(t, http.MethodGet, f.consolePath(game.ID), nil)
	cellID := decodeCells(t, body)[0].ID

	seen := map[string]bool{}
	for i := range 3 {
		status, body := f.serveConsole(t, http.MethodPost,
			f.consolePath(game.ID)+"/board/cells/"+cellID+"/swap", nil)
		if status != http.StatusOK {
			t.Fatalf("swap %d: status = %d (body %q)", i+1, status, body)
		}
		for _, c := range decodeCells(t, body) {
			if c.ID == cellID {
				if seen[c.Prompt] {
					t.Fatalf("swap %d dealt a question this cell has already had: %q", i+1, c.Prompt)
				}
				seen[c.Prompt] = true
			}
		}
	}
	if len(seen) != 3 {
		t.Fatalf("three swaps produced %d distinct questions", len(seen))
	}
}

// A category with nothing spare has to say so in words the host can act on,
// not fail silently or hand back the same question.
func TestSwapSaysSoWhenTheCategoryIsSpent(t *testing.T) {
	f := newFixture(t)
	// Exactly two per topic against a two-row board: everything the bank has
	// is already on the board.
	f.seedBank(topicSet(), 2)
	game := f.newGame(defaultSettings(), topicSet())

	_, body := f.serveConsole(t, http.MethodGet, f.consolePath(game.ID), nil)
	cells := decodeCells(t, body)
	if cells[0].Spares != 0 {
		t.Fatalf("a spent category reports %d spare, want 0", cells[0].Spares)
	}

	status, body := f.serveConsole(t, http.MethodPost,
		f.consolePath(game.ID)+"/board/cells/"+cells[0].ID+"/swap", nil)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body %q)", status, body)
	}
	if !strings.Contains(body, cells[0].Topic) {
		t.Fatalf("the refusal does not name the category: %q", body)
	}
}

// Once the room is playing, the cells are being chosen off a screen and the
// question behind one of them is not an edit anybody asked for.
func TestSwapRefusedOnceTheGameHasStarted(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())

	_, body := f.serveConsole(t, http.MethodGet, f.consolePath(game.ID), nil)
	cellID := decodeCells(t, body)[0].ID
	f.join(game.ID, "Bar Flies")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})

	status, body := f.serveConsole(t, http.MethodPost,
		f.consolePath(game.ID)+"/board/cells/"+cellID+"/swap", nil)
	if status != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body %q)", status, body)
	}
}

// A swap must never deal a question already sitting elsewhere on this board.
// The board's own unique index is on question id and would not catch the same
// question arriving from a second set, which the room certainly would.
func TestSwapNeverDuplicatesAQuestionOnTheBoard(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())

	_, body := f.serveConsole(t, http.MethodGet, f.consolePath(game.ID), nil)
	cells := decodeCells(t, body)
	for i := range 2 {
		_, body = f.serveConsole(t, http.MethodPost,
			f.consolePath(game.ID)+"/board/cells/"+cells[i].ID+"/swap", nil)
	}
	seen := map[string]string{}
	for _, c := range decodeCells(t, body) {
		if other, dup := seen[c.Prompt]; dup {
			t.Fatalf("%q is on the board twice (cells %s and %s)", c.Prompt, other, c.ID)
		}
		seen[c.Prompt] = c.ID
	}
}
