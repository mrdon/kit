package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/mcpauth"
	"github.com/mrdon/kit/internal/services"
)

func widgetAnalyticsMCPHandler(name string, _ *pgxpool.Pool, svc *services.Services) mcpserver.ToolHandlerFunc {
	switch name {
	case "list_widget_conversations":
		return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
			since, err := parseFlexibleMCPTime(req.GetString("since", ""))
			if err != nil {
				return mcp.NewToolResultError("Invalid 'since': " + err.Error()), nil
			}
			until, err := parseFlexibleMCPTime(req.GetString("until", ""))
			if err != nil {
				return mcp.NewToolResultError("Invalid 'until': " + err.Error()), nil
			}
			origin := req.GetString("origin", "")
			visitorID := req.GetString("visitor_id", "")
			limit := req.GetInt("limit", 0)
			convs, err := svc.WidgetAnalytics.List(ctx, caller, since, until, origin, visitorID, limit)
			if err != nil {
				return nil, err
			}
			if len(convs) == 0 {
				return mcp.NewToolResultText("No widget conversations match."), nil
			}
			var b strings.Builder
			fmt.Fprintf(&b, "Widget conversations (%d):\n", len(convs))
			for _, c := range convs {
				marker := ""
				if c.LooksUnanswered {
					marker = " [looks_unanswered]"
				}
				fmt.Fprintf(&b, "- [%s]%s started=%s msgs=%d origin=%s visitor=%s\n  first: %q\n",
					c.SessionID, marker, c.StartedAt.UTC().Format("2006-01-02 15:04"),
					c.MessageCount, c.Origin, c.VisitorID, c.FirstUserMessage)
			}
			return mcp.NewToolResultText(b.String()), nil
		})
	case "search_widget_conversations":
		return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
			query, _ := req.RequireString("query")
			since, err := parseFlexibleMCPTime(req.GetString("since", ""))
			if err != nil {
				return mcp.NewToolResultError("Invalid 'since': " + err.Error()), nil
			}
			until, err := parseFlexibleMCPTime(req.GetString("until", ""))
			if err != nil {
				return mcp.NewToolResultError("Invalid 'until': " + err.Error()), nil
			}
			limit := req.GetInt("limit", 0)
			hits, err := svc.WidgetAnalytics.Search(ctx, caller, query, since, until, limit)
			if err != nil {
				return nil, err
			}
			if len(hits) == 0 {
				return mcp.NewToolResultText("No matches."), nil
			}
			var b strings.Builder
			fmt.Fprintf(&b, "Widget conversation matches (%d):\n", len(hits))
			for _, h := range hits {
				fmt.Fprintf(&b, "- [%s] %s @ %s: %s\n",
					h.SessionID, h.MatchedRole, h.StartedAt.UTC().Format("2006-01-02 15:04"), h.Snippet)
			}
			return mcp.NewToolResultText(b.String()), nil
		})
	case "read_widget_conversation":
		return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
			idStr, _ := req.RequireString("session_id")
			id, err := uuid.Parse(idStr)
			if err != nil {
				return mcp.NewToolResultError("Invalid session ID."), nil
			}
			t, err := svc.WidgetAnalytics.Read(ctx, caller, id)
			if err != nil {
				if errors.Is(err, services.ErrNotFound) {
					return mcp.NewToolResultText("Conversation not found."), nil
				}
				return nil, err
			}
			var b strings.Builder
			fmt.Fprintf(&b, "Conversation [%s] started=%s origin=%s visitor=%s\n",
				t.SessionID, t.StartedAt.UTC().Format("2006-01-02 15:04"), t.Origin, t.VisitorID)
			for _, m := range t.Messages {
				fmt.Fprintf(&b, "  %s [%s]: %s\n", strings.ToUpper(m.Role), m.At.UTC().Format("15:04"), m.Text)
			}
			if len(t.ToolCalls) > 0 {
				b.WriteString("  tools: ")
				var names []string
				for _, tc := range t.ToolCalls {
					names = append(names, tc.Name)
				}
				b.WriteString(strings.Join(names, ", "))
				b.WriteByte('\n')
			}
			return mcp.NewToolResultText(b.String()), nil
		})
	default:
		return nil
	}
}

// parseFlexibleMCPTime accepts RFC3339 or YYYY-MM-DD; empty means "no
// bound" and returns nil.
func parseFlexibleMCPTime(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil //nolint:nilnil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return &t, nil
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return &t, nil
	}
	return nil, fmt.Errorf("expected RFC3339 or YYYY-MM-DD, got %q", s)
}
