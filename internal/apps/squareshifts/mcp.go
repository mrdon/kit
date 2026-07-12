package squareshifts

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/mcpauth"
	"github.com/mrdon/kit/internal/services"
)

func buildSquareShiftsMCPTools(a *App) []mcpserver.ServerTool {
	var out []mcpserver.ServerTool
	for _, meta := range squareShiftsTools {
		handler := squareShiftsMCPHandler(meta.Name, a)
		if handler == nil {
			continue
		}
		out = append(out, apps.MCPToolFromMeta(meta, handler))
	}
	return out
}

func squareShiftsMCPHandler(name string, a *App) mcpserver.ToolHandlerFunc {
	switch name {
	case "squareshifts_sync_now":
		return mcpSyncNow(a)
	case "squareshifts_status":
		return mcpStatus(a)
	default:
		return nil
	}
}

func mcpSyncNow(a *App) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, _ mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		sum, err := a.RunSync(ctx, caller.TenantID, "manual")
		if err != nil {
			return mcp.NewToolResultError(syncErrorMessage(err)), nil
		}
		return mcp.NewToolResultText(formatSummary(sum)), nil
	})
}

func mcpStatus(a *App) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, _ mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		msg, err := formatStatus(ctx, a, caller.TenantID)
		if err != nil {
			return nil, err
		}
		return mcp.NewToolResultText(msg), nil
	})
}
