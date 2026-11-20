package trivia

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/tools"
)

const bankTestCSV = `question,topics,answer,difficulty,source
"How many pizzas, in millions, does the US eat a day?",Food;Pizza,3000,7,https://example.com/a
How many feet tall is the tallest pizza oven?,Pizza,12,4,https://example.com/b
Bad row with a word answer,Pizza,lots,5,https://example.com/c
`

// bankSurfaces wires the agent registry and the MCP server over one fixture,
// so each test can drive a bank tool through both.
type bankSurfaces struct {
	f   *fixture
	reg *tools.Registry
	ec  *tools.ExecContext
	mcp context.Context
}

func newBankSurfaces(t *testing.T) *bankSurfaces {
	t.Helper()
	f := newFixture(t)
	user, err := models.GetOrCreateUser(f.ctx, f.pool, f.tenant.ID, "U_TRIVIA_BANK", "Host", "")
	if err != nil {
		t.Fatalf("creating user: %v", err)
	}
	reg := tools.NewRegistry(f.ctx, nil, false)
	registerAgentTools(reg, f.pool, f.svc)
	ec := &tools.ExecContext{Ctx: f.ctx, Pool: f.pool, Tenant: f.tenant, User: user}
	return &bankSurfaces{f: f, reg: reg, ec: ec, mcp: auth.WithCaller(context.Background(), ec.Caller())}
}

func (s *bankSurfaces) agent(t *testing.T, name string, in map[string]any) string {
	t.Helper()
	raw, _ := json.Marshal(in)
	out, err := s.reg.Execute(s.ec, name, raw)
	if err != nil {
		t.Fatalf("agent %s: %v", name, err)
	}
	return out
}

func TestImportPackThenListOnBothSurfaces(t *testing.T) {
	s := newBankSurfaces(t)

	// The import goes in over MCP, the way a harness running the trivia-pack
	// skill would send it.
	out, err := callMCPWith(s.mcp, buildMCPTools(s.f.pool, s.f.svc), "trivia_import_pack",
		map[string]any{"name": "Pizza", "csv": bankTestCSV, "notes": "Test pack."})
	if err != nil {
		t.Fatalf("mcp import: %v", err)
	}
	for _, want := range []string{"Imported 2 questions into *Pizza*", "average difficulty 5.5", "line 4", "Pizza (2)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("import summary missing %q:\n%s", want, out)
		}
	}

	in := map[string]any{"pack": "pizza", "questions": true}
	agentOut := s.agent(t, "trivia_list_questions", in)
	mcpOut, err := callMCPWith(s.mcp, buildMCPTools(s.f.pool, s.f.svc), "trivia_list_questions", in)
	if err != nil {
		t.Fatalf("mcp list: %v", err)
	}
	if agentOut != mcpOut {
		t.Fatalf("list differs between surfaces:\n--- agent ---\n%s\n--- mcp ---\n%s", agentOut, mcpOut)
	}
	for _, want := range []string{
		"*Pizza*: 2 questions, 2 never asked, average difficulty 5.5. Test pack.",
		"- [4] How many feet tall is the tallest pizza oven? = 12 (Pizza)",
		"= 3000 (Food; Pizza)",
	} {
		if !strings.Contains(agentOut, want) {
			t.Fatalf("listing missing %q:\n%s", want, agentOut)
		}
	}

	// Search matches question text or a topic, on any of the terms, and
	// lists only the hits.
	hits := s.agent(t, "trivia_list_questions", map[string]any{"search": "OVEN, nothing-matches-this"})
	if !strings.Contains(hits, "tallest pizza oven") || strings.Contains(hits, "pizzas, in millions") {
		t.Fatalf("search should list only the oven question:\n%s", hits)
	}
	if byTopic := s.agent(t, "trivia_list_questions", map[string]any{"search": "food"}); !strings.Contains(byTopic, "pizzas, in millions") {
		t.Fatalf("search should match on topic:\n%s", byTopic)
	}
	if none := s.agent(t, "trivia_list_questions", map[string]any{"search": "zebra"}); none != `No questions match "zebra".` {
		t.Fatalf("a search with no hits should say so, got:\n%s", none)
	}
}

func TestImportPackReplaceRules(t *testing.T) {
	s := newBankSurfaces(t)
	in := map[string]any{"name": "Pizza", "csv": bankTestCSV}
	s.agent(t, "trivia_import_pack", in)

	if out := s.agent(t, "trivia_import_pack", in); !strings.Contains(out, "already exists") {
		t.Fatalf("a second import without replace should be refused:\n%s", out)
	}

	in["replace"] = true
	if out := s.agent(t, "trivia_import_pack", in); !strings.Contains(out, "Imported 2 questions") {
		t.Fatalf("replacing a never-played pack should work:\n%s", out)
	}

	// Play a round from a pack, and it can no longer be replaced here.
	s.f.seedDataset("Played", topicSet(), 4)
	game := s.f.newGame(defaultSettings(), topicSet())
	team := s.f.join(game.ID, "Bar Flies")
	s.f.join(game.ID, "Quiz Khalifa")
	s.f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})
	s.f.playOneRound(game, map[uuid.UUID]string{team.ID: FormatValue(snapCorrect(t, s.f, game))})

	out := s.agent(t, "trivia_import_pack", map[string]any{"name": "played", "csv": bankTestCSV, "replace": true})
	if !strings.Contains(out, "has been played") && !strings.Contains(out, "still in play") {
		t.Fatalf("a played pack should not be replaceable here:\n%s", out)
	}
}
