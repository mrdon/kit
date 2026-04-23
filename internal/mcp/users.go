package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/mcpauth"
	"github.com/mrdon/kit/internal/services"
)

func userMCPHandler(name string, _ *pgxpool.Pool, svc *services.Services) mcpserver.ToolHandlerFunc {
	if name != "find_user" {
		return nil
	}
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		query, _ := req.RequireString("query")
		users, err := svc.Users.Find(ctx, caller, query)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(users) == 0 {
			return mcp.NewToolResultText(fmt.Sprintf("No users found matching %q.", query)), nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Found %d user(s):\n", len(users))
		for _, u := range users {
			b.WriteString("- " + services.FormatUserLine(&u) + "\n")
		}
		return mcp.NewToolResultText(b.String()), nil
	})
}
