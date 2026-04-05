package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/services"
)

func ruleMCPHandler(name string, _ *pgxpool.Pool, svc *services.Services, caller *services.Caller) mcpserver.ToolHandlerFunc {
	switch name {
	case "list_rules":
		return func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			rules, err := svc.Rules.List(ctx, caller)
			if err != nil {
				return nil, err
			}
			if len(rules) == 0 {
				return mcp.NewToolResultText("No rules defined yet."), nil
			}
			var b strings.Builder
			for _, r := range rules {
				fmt.Fprintf(&b, "- [%s] (priority %d) %s\n", r.ID, r.Priority, r.Content)
			}
			return mcp.NewToolResultText(b.String()), nil
		}
	case "create_rule":
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			content, _ := req.RequireString("content")
			scope := req.GetString("scope", "tenant")
			scopeValue := req.GetString("scope_value", "*")
			priority := req.GetInt("priority", 0)
			rule, err := svc.Rules.Create(ctx, caller, content, priority, scope, scopeValue)
			if err != nil {
				return nil, err
			}
			return mcp.NewToolResultText(fmt.Sprintf("Rule created (ID: %s).", rule.ID)), nil
		}
	case "update_rule":
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			idStr, _ := req.RequireString("rule_id")
			ruleID, err := uuid.Parse(idStr)
			if err != nil {
				return mcp.NewToolResultError("Invalid rule ID."), nil
			}
			content, _ := req.RequireString("content")
			if err := svc.Rules.Update(ctx, caller, ruleID, content); err != nil {
				return nil, err
			}
			return mcp.NewToolResultText("Rule updated."), nil
		}
	case "delete_rule":
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			idStr, _ := req.RequireString("rule_id")
			ruleID, err := uuid.Parse(idStr)
			if err != nil {
				return mcp.NewToolResultError("Invalid rule ID."), nil
			}
			if err := svc.Rules.Delete(ctx, caller, ruleID); err != nil {
				return nil, err
			}
			return mcp.NewToolResultText("Rule deleted."), nil
		}
	default:
		return nil
	}
}
