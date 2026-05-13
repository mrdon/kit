package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/mcpauth"
	"github.com/mrdon/kit/internal/services"
)

func widgetTokenMCPHandler(name string, _ *pgxpool.Pool, svc *services.Services) mcpserver.ToolHandlerFunc {
	switch name {
	case "create_widget_token":
		return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
			origins := req.GetStringSlice("allowed_origins", nil)
			created, err := svc.WidgetTokens.Create(ctx, caller, origins)
			if err != nil {
				return nil, err
			}
			var b strings.Builder
			fmt.Fprintf(&b, "Widget token created (ID: %s).\n\n", created.ID)
			fmt.Fprintf(&b, "Token (shown once — save it now): %s\n\n", created.Plaintext)
			b.WriteString("Embed snippet:\n")
			b.WriteString(created.EmbedSnippet)
			b.WriteString("\n\nAllowed origins:\n")
			for _, o := range created.AllowedOrigin {
				fmt.Fprintf(&b, "- %s\n", o)
			}
			return mcp.NewToolResultText(b.String()), nil
		})
	case "list_widget_tokens":
		return mcpauth.WithCaller(func(ctx context.Context, _ mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
			tokens, err := svc.WidgetTokens.List(ctx, caller)
			if err != nil {
				return nil, err
			}
			if len(tokens) == 0 {
				return mcp.NewToolResultText("No active widget tokens."), nil
			}
			var b strings.Builder
			b.WriteString("Active widget tokens:\n")
			for _, t := range tokens {
				fmt.Fprintf(&b, "- [%s] %s · origins: %s · created: %s",
					t.ID, t.Placeholder, strings.Join(t.AllowedOrigins, ", "), t.CreatedAt)
				if t.LastUsedAt != "" {
					fmt.Fprintf(&b, " · last used: %s", t.LastUsedAt)
				}
				b.WriteByte('\n')
			}
			return mcp.NewToolResultText(b.String()), nil
		})
	case "revoke_widget_token":
		return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
			idStr, _ := req.RequireString("token_id")
			tokenID, err := uuid.Parse(idStr)
			if err != nil {
				return mcp.NewToolResultError("Invalid token ID."), nil
			}
			if err := svc.WidgetTokens.Revoke(ctx, caller, tokenID); err != nil {
				return nil, err
			}
			return mcp.NewToolResultText("Widget token revoked."), nil
		})
	default:
		return nil
	}
}
