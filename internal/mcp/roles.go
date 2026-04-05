package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/services"
)

func roleMCPHandler(name string, _ *pgxpool.Pool, svc *services.Services, caller *services.Caller) mcpserver.ToolHandlerFunc {
	switch name {
	case "list_roles":
		return func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			roles, err := svc.Roles.List(ctx, caller)
			if err != nil {
				return nil, err
			}
			if len(roles) == 0 {
				return mcp.NewToolResultText("No roles defined yet."), nil
			}
			var b strings.Builder
			for _, r := range roles {
				desc := ""
				if r.Description != nil {
					desc = " — " + *r.Description
				}
				fmt.Fprintf(&b, "- %s%s\n", r.Name, desc)
			}
			return mcp.NewToolResultText(b.String()), nil
		}
	case "list_role_members":
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			roleName, _ := req.RequireString("role_name")
			members, err := svc.Roles.ListMembers(ctx, caller, roleName)
			if err != nil {
				return nil, err
			}
			if len(members) == 0 {
				return mcp.NewToolResultText("No users assigned to role '" + roleName + "'."), nil
			}
			var b strings.Builder
			for _, m := range members {
				name := m.SlackUserID
				if m.DisplayName != nil {
					name = *m.DisplayName + " (" + m.SlackUserID + ")"
				}
				b.WriteString("- " + name + "\n")
			}
			return mcp.NewToolResultText(b.String()), nil
		}
	case "create_role":
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			name, _ := req.RequireString("name")
			desc := req.GetString("description", "")
			role, err := svc.Roles.Create(ctx, caller, name, desc)
			if err != nil {
				return nil, err
			}
			return mcp.NewToolResultText(fmt.Sprintf("Role '%s' created.", role.Name)), nil
		}
	case "assign_role":
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			slackUserID, _ := req.RequireString("slack_user_id")
			roleName, _ := req.RequireString("role_name")
			if err := svc.Roles.Assign(ctx, caller, slackUserID, roleName); err != nil {
				return nil, err
			}
			return mcp.NewToolResultText(fmt.Sprintf("Role '%s' assigned to %s.", roleName, slackUserID)), nil
		}
	case "unassign_role":
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			slackUserID, _ := req.RequireString("slack_user_id")
			roleName, _ := req.RequireString("role_name")
			err := svc.Roles.Unassign(ctx, caller, slackUserID, roleName)
			if errors.Is(err, services.ErrNotFound) {
				return mcp.NewToolResultError("User not found."), nil
			}
			if err != nil {
				return nil, err
			}
			return mcp.NewToolResultText(fmt.Sprintf("Role '%s' removed from %s.", roleName, slackUserID)), nil
		}
	case "update_role":
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			name, _ := req.RequireString("name")
			desc, _ := req.RequireString("description")
			if err := svc.Roles.Update(ctx, caller, name, desc); err != nil {
				return nil, err
			}
			return mcp.NewToolResultText(fmt.Sprintf("Role '%s' updated.", name)), nil
		}
	case "delete_role":
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			name, _ := req.RequireString("name")
			if err := svc.Roles.Delete(ctx, caller, name); err != nil {
				return nil, err
			}
			return mcp.NewToolResultText(fmt.Sprintf("Role '%s' deleted.", name)), nil
		}
	default:
		return nil
	}
}
