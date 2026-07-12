package square

import (
	"context"
	"errors"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/mcpauth"
	"github.com/mrdon/kit/internal/services"
)

func buildSquareMCPTools(a *App) []mcpserver.ServerTool {
	var out []mcpserver.ServerTool
	for _, meta := range squareTools {
		handler := squareMCPHandler(meta.Name, a)
		if handler == nil {
			continue
		}
		out = append(out, apps.MCPToolFromMeta(meta, handler))
	}
	return out
}

func squareMCPHandler(name string, a *App) mcpserver.ToolHandlerFunc {
	switch name {
	case "square_list_shifts":
		return mcpListShifts(a)
	default:
		return nil
	}
}

func mcpListShifts(a *App) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		start, end, err := resolveRange(caller.Timezone, req.GetString("start", ""), req.GetString("end", ""))
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		shifts, err := a.ListPublishedShifts(ctx, caller.TenantID, start, end)
		if err != nil {
			if errors.Is(err, ErrNotConfigured) {
				return mcp.NewToolResultError("Square isn't connected yet. Configure the Square integration first."), nil
			}
			if errors.Is(err, ErrNotReady) {
				return mcp.NewToolResultError("Square app credentials aren't configured on this server."), nil
			}
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatShifts(shifts)), nil
	})
}
