package trivia

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/services"
)

// caller builds a Caller for this fixture's tenant.
func (f *fixture) caller() *services.Caller {
	return &services.Caller{TenantID: f.tenant.ID, UserID: uuid.New()}
}

// The morning-after question: how did last night go.
func TestTriviaStatusSummarisesRecentGames(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())
	f.do(game.ID, ActionRequest{Action: ActionOpenLobby, FromPhase: PhaseSetup})
	a := f.join(game.ID, "Bar Flies")
	f.join(game.ID, "Quiz Khalifa")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})
	f.playOneRound(game, map[uuid.UUID]string{a.ID: FormatValue(snapCorrect(t, f, game))})

	out, err := dispatchCore(f.ctx, f.caller(), f.pool, f.svc, "trivia_status", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("trivia_status: %v", err)
	}
	for _, want := range []string{game.Name, "2 teams", "leading: Bar Flies", "1 of 10 questions done"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status is missing %q:\n%s", want, out)
		}
	}
}

// A finished game's recap. The answer IS included here, deliberately: this is
// somebody asking about last night, not a live board.
func TestTriviaResultsGivesTheLeaderboardAndRecap(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	s := defaultSettings()
	s.BoardColumns, s.BoardRows = 1, 1
	s.CellValues = []int{500}
	s.FinalWager = false
	game := f.newGame(s, []string{"space"})
	f.do(game.ID, ActionRequest{Action: ActionOpenLobby, FromPhase: PhaseSetup})
	a := f.join(game.ID, "Bar Flies")
	f.join(game.ID, "Quiz Khalifa")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})
	correct := snapCorrect(t, f, game)
	f.playOneRound(game, map[uuid.UUID]string{a.ID: FormatValue(correct)})
	f.do(game.ID, ActionRequest{Action: ActionNext, FromPhase: PhaseScoring})

	out, err := dispatchCore(f.ctx, f.caller(), f.pool, f.svc,
		"trivia_results", json.RawMessage(`{"game":"`+game.Name+`"}`))
	if err != nil {
		t.Fatalf("trivia_results: %v", err)
	}
	for _, want := range []string{
		"finished", "Final standings", "1. Bar Flies — $500", "Quiz Khalifa — $0",
		"Round by round", "Bar Flies took $500",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("results is missing %q:\n%s", want, out)
		}
	}
}

// With no game named, the most recent one is used — which is what somebody
// asking "how did it go" means.
func TestTriviaResultsDefaultsToTheMostRecentGame(t *testing.T) {
	f := newFixture(t)
	f.newGame(defaultSettings(), nil)
	newest := f.newGame(defaultSettings(), nil)

	out, err := dispatchCore(f.ctx, f.caller(), f.pool, f.svc, "trivia_results", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("trivia_results: %v", err)
	}
	if !strings.Contains(out, newest.Name) {
		t.Fatalf("did not default to the newest game:\n%s", out)
	}
}

// A name that is not a game name bounces with a readable message rather than
// reaching a query.
func TestTriviaResultsRejectsAJunkName(t *testing.T) {
	f := newFixture(t)
	f.newGame(defaultSettings(), nil)
	_, err := dispatchCore(f.ctx, f.caller(), f.pool, f.svc,
		"trivia_results", json.RawMessage(`{"game":"../../etc/passwd"}`))
	if err == nil {
		t.Fatal("a path-shaped game name was accepted")
	}
	if !strings.Contains(err.Error(), "brave-otter-lamp") {
		t.Fatalf("error %q does not say what a game name looks like", err)
	}
}

// CLAUDE.md's parity rule, made structural: both surfaces are thin wrappers
// over dispatchCore, so this asserts the metadata matches and that neither
// surface has a tool the other lacks.
func TestAgentAndMCPSurfacesAgree(t *testing.T) {
	f := newFixture(t)
	app := &App{pool: f.pool, svc: f.svc}

	metas := app.ToolMetas()
	if len(metas) != 2 {
		t.Fatalf("ToolMetas returned %d tools, want 2", len(metas))
	}
	mcpTools := app.RegisterMCPTools(f.pool, nil)
	if len(mcpTools) != len(metas) {
		t.Fatalf("MCP exposes %d tools but there are %d metas", len(mcpTools), len(metas))
	}
	byName := map[string]bool{}
	for _, tool := range mcpTools {
		byName[tool.Tool.Name] = true
	}
	for _, m := range metas {
		if !byName[m.Name] {
			t.Fatalf("%s is in the shared metadata but not on the MCP surface", m.Name)
		}
		if m.Description == "" {
			t.Fatalf("%s has no description", m.Name)
		}
		// Both surfaces reach the same dispatcher, so an unknown name must
		// fail identically rather than silently no-op on one side.
		if _, err := dispatchCore(f.ctx, f.caller(), f.pool, f.svc, m.Name+"_nope", nil); err == nil {
			t.Fatalf("dispatchCore accepted an unknown tool name")
		}
	}
}

// Both tools are read-only. If one ever gains a mutating path this test is
// where somebody has to think about it.
func TestTriviaToolsAreReadOnly(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())
	before := f.reload(game.ID).StateVersion

	for _, name := range []string{"trivia_status", "trivia_results"} {
		if _, err := dispatchCore(f.ctx, f.caller(), f.pool, f.svc, name, json.RawMessage(`{}`)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if after := f.reload(game.ID).StateVersion; after != before {
		t.Fatalf("a read-only tool moved state_version from %d to %d", before, after)
	}
}
