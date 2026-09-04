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

// withBoard wraps the common shape: resolve the caller, refuse when the app
// is unconfigured, and map an error onto a tool error rather than a transport
// one so the caller reads why.
func withBoard(a *App, run func(context.Context, mcp.CallToolRequest, *services.Caller) (string, error)) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		if a.svc == nil {
			return mcp.NewToolResultError("menu app is not configured"), nil
		}
		msg, err := run(ctx, req, caller)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(msg), nil
	})
}

func mcpHandler(name string, pool *pgxpool.Pool, a *App) mcpserver.ToolHandlerFunc {
	switch name {
	case "set_menu_source":
		return withBoard(a, func(ctx context.Context, req mcp.CallToolRequest, c *services.Caller) (string, error) {
			return applySource(ctx, pool, a, c.TenantID, setSourceArgs{BoardID: req.GetString("board_id", "")})
		})
	case "set_menu_board":
		return withBoard(a, func(ctx context.Context, req mcp.CallToolRequest, c *services.Caller) (string, error) {
			return saveBoard(ctx, pool, a, c.TenantID, setBoardArgs{
				Name:    req.GetString("name", ""),
				Payload: req.GetString("payload", ""),
			})
		})
	case "set_menu_asset":
		return withBoard(a, func(ctx context.Context, req mcp.CallToolRequest, c *services.Caller) (string, error) {
			return saveAsset(ctx, pool, a.fetcher, c.TenantID, setAssetArgs{
				Key: req.GetString("key", ""),
				URL: req.GetString("url", ""),
			})
		})
	case "set_menu_print":
		return withBoard(a, func(ctx context.Context, req mcp.CallToolRequest, c *services.Caller) (string, error) {
			return savePrintConfig(ctx, pool, c.TenantID, setPrintArgs{
				Payload: req.GetString("payload", ""),
			})
		})
	case "get_menu_board":
		return withBoard(a, func(ctx context.Context, _ mcp.CallToolRequest, c *services.Caller) (string, error) {
			return describeBoard(ctx, pool, a, c.TenantID)
		})
	default:
		return nil
	}
}
