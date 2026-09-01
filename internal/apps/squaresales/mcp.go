package squaresales

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/mcpauth"
	"github.com/mrdon/kit/internal/services"
)

func buildSquareSalesMCPTools(a *App) []mcpserver.ServerTool {
	var out []mcpserver.ServerTool
	for _, meta := range squareSalesTools {
		handler := squareSalesMCPHandler(meta.Name, a)
		if handler == nil {
			continue
		}
		out = append(out, apps.MCPToolFromMeta(meta, handler))
	}
	return out
}

func squareSalesMCPHandler(name string, a *App) mcpserver.ToolHandlerFunc {
	switch name {
	case "squaresales_sync_now":
		return mcpSyncNow(a)
	case "squaresales_post_card_now":
		return mcpPostCard(a)
	case "squaresales_status":
		return mcpStatus(a)
	default:
		return nil
	}
}

func mcpSyncNow(a *App) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, _ mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		sum, err := a.RunSync(ctx, caller.TenantID, "manual")
		if err != nil {
			return mcp.NewToolResultError(salesErrorMessage(err)), nil
		}
		return mcp.NewToolResultText(formatSyncSummary(sum)), nil
	})
}

func mcpPostCard(a *App) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		args := postCardArgs{
			Date:    req.GetString("date", ""),
			Preview: req.GetBool("preview", false),
		}
		out, err := postCard(ctx, a, caller.TenantID, args)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(out), nil
	})
}

func mcpStatus(a *App) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, _ mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		out, err := formatStatus(ctx, a, caller.TenantID)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(out), nil
	})
}
