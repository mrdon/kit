package trivia

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/tools"
)

// The parity check CLAUDE.md asks for, run rather than asserted by
// inspection: drive the same question through the agent registry and through
// the MCP handler, and require the two to produce byte-identical text.
//
// Both surfaces are thin wrappers over dispatchCore, so this should be true
// by construction — which is exactly why it is worth a test. A future change
// that adds surface-specific formatting on one side fails here.
func TestAgentAndMCPReturnByteIdenticalText(t *testing.T) {
	f := newFixture(t)
	f.seedBank(topicSet(), 4)
	game := f.newGame(defaultSettings(), topicSet())
	a := f.join(game.ID, "Bar Flies")
	f.join(game.ID, "Quiz Khalifa")
	f.do(game.ID, ActionRequest{Action: ActionStart, FromPhase: PhaseLobby})
	f.playOneRound(game, map[uuid.UUID]string{a.ID: FormatValue(snapCorrect(t, f, game))})

	user, err := models.GetOrCreateUser(f.ctx, f.pool, f.tenant.ID, "U_TRIVIA_TEST", "Host", "")
	if err != nil {
		t.Fatalf("creating user: %v", err)
	}

	// The agent side: a real registry, executed through a real ExecContext.
	reg := tools.NewRegistry(f.ctx, nil, false)
	registerAgentTools(reg, f.pool, f.svc)
	ec := &tools.ExecContext{Ctx: f.ctx, Pool: f.pool, Tenant: f.tenant, User: user}

	// The MCP side: the caller comes off the context, as the server puts it
	// there.
	mcpCtx := auth.WithCaller(context.Background(), ec.Caller())
	mcpTools := buildMCPTools(f.pool, f.svc)

	for _, name := range []string{"trivia_status", "trivia_results"} {
		agentOut, err := reg.Execute(ec, name, json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("agent %s: %v", name, err)
		}
		mcpOut, err := callMCP(mcpCtx, mcpTools, name)
		if err != nil {
			t.Fatalf("mcp %s: %v", name, err)
		}
		if agentOut == "" {
			t.Fatalf("%s returned nothing", name)
		}
		if agentOut != mcpOut {
			t.Fatalf("%s differs between surfaces:\n--- agent ---\n%s\n--- mcp ---\n%s",
				name, agentOut, mcpOut)
		}
		t.Logf("%s, identical on both surfaces:\n%s", name, agentOut)
	}
}

func callMCP(ctx context.Context, serverTools []mcpserver.ServerTool, name string) (string, error) {
	for _, tool := range serverTools {
		if tool.Tool.Name != name {
			continue
		}
		var req mcp.CallToolRequest
		req.Params.Name = name
		req.Params.Arguments = map[string]any{}
		res, err := tool.Handler(ctx, req)
		if err != nil {
			return "", err
		}
		var out string
		for _, c := range res.Content {
			if tc, ok := c.(mcp.TextContent); ok {
				out = tc.Text
			}
		}
		return out, nil
	}
	return "", nil
}
