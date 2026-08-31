package menu

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/mcpauth"
	"github.com/mrdon/kit/internal/services"
)

func registerMCPTools(pool *pgxpool.Pool, _ *services.Services, a *App) []mcpserver.ServerTool {
	var out []mcpserver.ServerTool
	for _, meta := range toolMetas() {
		handler := mcpHandler(meta.Name, pool, a)
		if handler == nil {
			continue
		}
		out = append(out, apps.MCPToolFromMeta(meta, handler))
	}
	return out
}

func mcpHandler(name string, pool *pgxpool.Pool, a *App) mcpserver.ToolHandlerFunc {
	switch name {
	case "set_menu_board":
		return mcpSetBoard(pool, a)
	case "get_menu_board":
		return mcpGetBoard(pool, a)
	default:
		return nil
	}
}

func mcpSetBoard(pool *pgxpool.Pool, a *App) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		if a.svc == nil {
			return mcp.NewToolResultError("menu app is not configured"), nil
		}
		args := setBoardArgs{
			Key:     req.GetString("key", ""),
			Name:    req.GetString("name", ""),
			Payload: req.GetString("payload", ""),
		}
		msg, err := saveBoard(ctx, pool, a, caller.TenantID, args)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(msg), nil
	})
}

func mcpGetBoard(pool *pgxpool.Pool, a *App) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		if a.svc == nil {
			return mcp.NewToolResultError("menu app is not configured"), nil
		}
		msg, err := listBoards(ctx, pool, a, caller.TenantID, getBoardArgs{Key: req.GetString("key", "")})
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(msg), nil
	})
}
