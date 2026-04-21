package integrations

import (
	"context"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/mcpauth"
	"github.com/mrdon/kit/internal/services"
)

func buildMCPTools(a *App) []mcpserver.ServerTool {
	var out []mcpserver.ServerTool
	for _, meta := range services.IntegrationTools {
		handler := mcpHandler(a, meta.Name)
		if handler == nil {
			continue
		}
		out = append(out, apps.MCPToolFromMeta(meta, handler))
	}
	return out
}

func mcpHandler(a *App, name string) mcpserver.ToolHandlerFunc {
	switch name {
	case "configure_integration":
		return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
			provider := req.GetString("provider", "")
			authType := req.GetString("auth_type", "")
			msg, err := a.configureIntegration(ctx, caller, provider, authType)
			if err != nil {
				return nil, err
			}
			return mcp.NewToolResultText(msg), nil
		})
	case "check_integration_status":
		return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
			idStr, _ := req.RequireString("pending_id")
			pendingID, err := uuid.Parse(idStr)
			if err != nil {
				return mcp.NewToolResultError("Invalid pending_id UUID."), nil
			}
			msg, err := a.checkStatus(ctx, caller, pendingID)
			if err != nil {
				return nil, err
			}
			return mcp.NewToolResultText(msg), nil
		})
	case "list_integrations":
		return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			all := false
			if b, ok := args["all"].(bool); ok {
				all = b
			}
			msg, err := a.listIntegrations(ctx, caller, all)
			if err != nil {
				return nil, err
			}
			return mcp.NewToolResultText(msg), nil
		})
	case "delete_integration":
		return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
			idStr, _ := req.RequireString("integration_id")
			id, err := uuid.Parse(idStr)
			if err != nil {
				return mcp.NewToolResultError("Invalid integration_id UUID."), nil
			}
			msg, err := a.deleteIntegration(ctx, caller, id)
			if err != nil {
				return nil, err
			}
			return mcp.NewToolResultText(msg), nil
		})
	case "list_integration_types":
		return mcpauth.WithCaller(func(_ context.Context, _ mcp.CallToolRequest, _ *services.Caller) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(renderTypeCatalog()), nil
		})
	}
	return nil
}
