package posters

import (
	"context"
	"encoding/base64"
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/mcpauth"
	"github.com/mrdon/kit/internal/services"
)

// buildMCPTools wires every poster tool plus the MCP-only indexing tools
// onto the MCP surface. Same dispatchCore as the agent; renders come back
// as image content so the harness's model can look at them.
func (a *App) buildMCPTools() []mcpserver.ServerTool {
	var out []mcpserver.ServerTool
	all := allTools()
	for _, meta := range all {
		name := meta.Name
		handler := mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
			raw, err := json.Marshal(req.GetArguments())
			if err != nil {
				return mcp.NewToolResultError("Could not parse input."), nil
			}
			res, err := a.dispatchCore(ctx, caller, name, raw)
			if err != nil {
				return nil, err
			}
			if len(res.Images) == 0 {
				return mcp.NewToolResultText(res.Text), nil
			}
			result := &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent(res.Text)}}
			for _, img := range res.Images {
				result.Content = append(result.Content, mcp.NewImageContent(base64.StdEncoding.EncodeToString(img.Data), img.Mime))
			}
			return result, nil
		})
		out = append(out, apps.MCPToolFromMeta(meta, handler))
	}
	return out
}
