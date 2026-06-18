package expense

import (
	"context"
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/mcpauth"
	"github.com/mrdon/kit/internal/services"
)

// buildExpenseMCPTools wires every expense tool onto the MCP surface. Each
// handler marshals the MCP arguments back to JSON and runs the SAME core
// function the agent uses (dispatchCore), so the two surfaces stay in parity by
// construction.
func buildExpenseMCPTools(svc *ExpenseService) []mcpserver.ServerTool {
	var result []mcpserver.ServerTool
	for _, meta := range expenseTools {
		name := meta.Name
		handler := mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
			raw, err := json.Marshal(req.GetArguments())
			if err != nil {
				return mcp.NewToolResultError("Could not parse input."), nil
			}
			text, err := dispatchCore(ctx, caller, svc, name, raw)
			if err != nil {
				return nil, err
			}
			return mcp.NewToolResultText(text), nil
		})
		result = append(result, apps.MCPToolFromMeta(meta, handler))
	}
	return result
}
