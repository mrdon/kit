package googlecalendar

import (
	"context"
	"errors"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/mcpauth"
	"github.com/mrdon/kit/internal/services"
)

func buildGoogleCalendarMCPTools(a *App) []mcpserver.ServerTool {
	var out []mcpserver.ServerTool
	for _, meta := range googleCalendarTools {
		handler := googleCalendarMCPHandler(meta.Name, a)
		if handler == nil {
			continue
		}
		out = append(out, apps.MCPToolFromMeta(meta, handler))
	}
	return out
}

func googleCalendarMCPHandler(name string, a *App) mcpserver.ToolHandlerFunc {
	switch name {
	case "gcal_check":
		return mcpCheck(a)
	default:
		return nil
	}
}

func mcpCheck(a *App) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, _ mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		msg, err := a.CheckWriteAccess(ctx, caller.TenantID)
		if err != nil {
			if errors.Is(err, ErrNotConfigured) {
				return mcp.NewToolResultError("Google Calendar isn't connected yet. Configure it first."), nil
			}
			if errors.Is(err, ErrNotReady) {
				return mcp.NewToolResultError("Google Calendar app isn't configured on this server."), nil
			}
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(msg), nil
	})
}
