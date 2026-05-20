package email

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/mcpauth"
	"github.com/mrdon/kit/internal/services"
)

// buildEmailMCPTools exposes the read-side IMAP tools on MCP. send_email is
// intentionally excluded — it's PolicyGate and must only reach SMTP via
// tools.Registry.Execute (see .claude/skills/gated-tools-guide.md).
func buildEmailMCPTools(a *App) []mcpserver.ServerTool {
	var result []mcpserver.ServerTool
	for _, meta := range emailTools {
		handler := emailMCPHandler(meta.Name, a)
		if handler == nil {
			continue
		}
		result = append(result, apps.MCPToolFromMeta(meta, handler))
	}
	return result
}

func emailMCPHandler(name string, a *App) mcpserver.ToolHandlerFunc {
	switch name {
	case "search_emails":
		return mcpSearchEmails(a)
	case "read_email":
		return mcpReadEmail(a)
	case "mark_read":
		return mcpMarkRead(a)
	default:
		// send_email is PolicyGate — omitted on purpose.
		return nil
	}
}

func mcpSearchEmails(a *App) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		query := req.GetString("query", "")
		folder := req.GetString("folder", "")
		sinceStr := req.GetString("since", "")
		unreadOnly := req.GetBool("unread_only", false)
		limit := req.GetInt("limit", 0)

		var since time.Time
		if sinceStr != "" {
			d, err := time.Parse("2006-01-02", sinceStr)
			if err != nil {
				return mcp.NewToolResultError("Invalid 'since' date. Use YYYY-MM-DD."), nil
			}
			since = d
		}

		acct, err := LoadAccount(ctx, a.pool, a.enc, caller.TenantID, caller.UserID)
		if errors.Is(err, ErrNotConfigured) {
			return mcp.NewToolResultError(notConfiguredMsg), nil
		}
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		summaries, err := imapSearch(ctx, acct, query, folder, since, unreadOnly, limit)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if len(summaries) == 0 {
			return mcp.NewToolResultText("No messages matched."), nil
		}
		return mcp.NewToolResultText(formatSummaries(summaries)), nil
	})
}

func mcpReadEmail(a *App) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		uid := uint32(req.GetInt("uid", 0))
		if uid == 0 {
			return mcp.NewToolResultError("uid is required."), nil
		}
		folder := req.GetString("folder", "")

		acct, err := LoadAccount(ctx, a.pool, a.enc, caller.TenantID, caller.UserID)
		if errors.Is(err, ErrNotConfigured) {
			return mcp.NewToolResultError(notConfiguredMsg), nil
		}
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		msg, err := imapFetch(ctx, acct, uid, folder)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(formatMessage(msg)), nil
	})
}

func mcpMarkRead(a *App) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		uid := uint32(req.GetInt("uid", 0))
		if uid == 0 {
			return mcp.NewToolResultError("uid is required."), nil
		}
		read := req.GetBool("read", true)
		folder := req.GetString("folder", "")

		acct, err := LoadAccount(ctx, a.pool, a.enc, caller.TenantID, caller.UserID)
		if errors.Is(err, ErrNotConfigured) {
			return mcp.NewToolResultError(notConfiguredMsg), nil
		}
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		if err := imapMarkRead(ctx, acct, uid, read, folder); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if read {
			return mcp.NewToolResultText(fmt.Sprintf("Marked uid %d as read.", uid)), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("Marked uid %d as unread.", uid)), nil
	})
}
