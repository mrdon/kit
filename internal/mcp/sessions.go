package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/mcpauth"
	"github.com/mrdon/kit/internal/services"
)

func sessionMCPHandler(name string, _ *pgxpool.Pool, svc *services.Services) mcpserver.ToolHandlerFunc {
	switch name {
	case "list_sessions":
		return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
			limit := req.GetInt("limit", 20)
			sessions, err := svc.Sessions.List(ctx, caller, limit)
			if errors.Is(err, services.ErrForbidden) {
				return mcp.NewToolResultError("Admin access required."), nil
			}
			if err != nil {
				return nil, err
			}
			if len(sessions) == 0 {
				return mcp.NewToolResultText("No sessions found."), nil
			}
			var b strings.Builder
			for _, s := range sessions {
				b.WriteString(services.FormatSession(&s))
				b.WriteByte('\n')
			}
			return mcp.NewToolResultText(b.String()), nil
		})
	case "get_session_events":
		return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
			idStr, _ := req.RequireString("session_id")
			sessionID, err := uuid.Parse(idStr)
			if err != nil {
				return mcp.NewToolResultError("Invalid session ID."), nil
			}
			events, err := svc.Sessions.GetEvents(ctx, caller, sessionID)
			if errors.Is(err, services.ErrForbidden) {
				return mcp.NewToolResultError("Admin access required."), nil
			}
			if err != nil {
				return nil, err
			}
			if len(events) == 0 {
				return mcp.NewToolResultText(fmt.Sprintf("No events found for session %s.", sessionID)), nil
			}
			return mcp.NewToolResultText(services.FormatSessionEvents(events)), nil
		})
	default:
		return nil
	}
}
