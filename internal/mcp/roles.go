package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/mcpauth"
	"github.com/mrdon/kit/internal/services"
)

func roleMCPHandler(name string, _ *pgxpool.Pool, svc *services.Services) mcpserver.ToolHandlerFunc {
	switch name {
	case "list_roles":
		return mcpauth.WithCaller(func(ctx context.Context, _ mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
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
		})
	case "list_role_members":
		return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
			roleName, _ := req.RequireString("role_name")
			text, err := svc.Roles.RoleMembersText(ctx, caller, roleName)
			if err != nil {
				return nil, err
			}
			return mcp.NewToolResultText(text), nil
		})
	case "create_role":
		return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
			name, _ := req.RequireString("name")
			desc := req.GetString("description", "")
			role, err := svc.Roles.Create(ctx, caller, name, desc)
			if err != nil {
				return nil, err
			}
			return mcp.NewToolResultText(fmt.Sprintf("Role '%s' created.", role.Name)), nil
		})
	case "assign_role", "unassign_role":
		assign := name == "assign_role"
		return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
			roleName, _ := req.RequireString("role_name")
			text, err := svc.Roles.ChangeRole(ctx, caller, req.GetString("slack_user_id", ""), req.GetString("device", ""), roleName, assign)
			if err != nil {
				return nil, err
			}
			return mcp.NewToolResultText(text), nil
		})
	case "update_role":
		return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
			name, _ := req.RequireString("name")
			desc, _ := req.RequireString("description")
			if err := svc.Roles.Update(ctx, caller, name, desc); err != nil {
				return nil, err
			}
			return mcp.NewToolResultText(fmt.Sprintf("Role '%s' updated.", name)), nil
		})
	case "delete_role":
		return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
			name, _ := req.RequireString("name")
			force := false
			if v, ok := req.GetArguments()["force"].(bool); ok {
				force = v
			}
			if err := svc.Roles.Delete(ctx, caller, name, force); err != nil {
				if errors.Is(err, services.ErrRoleHasImpact) {
					return mcp.NewToolResultError(err.Error() + ". Re-run with force=true to confirm."), nil
				}
				return nil, err
			}
			return mcp.NewToolResultText(fmt.Sprintf("Role '%s' deleted.", name)), nil
		})
	default:
		return nil
	}
}
