package netlify

import (
	"context"
	"errors"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/mcpauth"
	"github.com/mrdon/kit/internal/services"
)

func buildNetlifyMCPTools(svc *Service) []mcpserver.ServerTool {
	var result []mcpserver.ServerTool
	for _, meta := range netlifyTools {
		handler := mcpHandlerFor(meta.Name, svc)
		if handler == nil {
			continue
		}
		result = append(result, apps.MCPToolFromMeta(meta, handler))
	}
	return result
}

func mcpHandlerFor(name string, svc *Service) mcpserver.ToolHandlerFunc {
	if name == "netlify_request_change" {
		return mcpRequestChange(svc)
	}
	return nil
}

func mcpRequestChange(svc *Service) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		prompt, err := req.RequireString("prompt")
		if err != nil {
			return mcp.NewToolResultError("prompt is required"), nil
		}
		res, rerr := svc.RequestChange(ctx, caller.TenantID, ChangeRequest{Prompt: prompt})
		if rerr != nil {
			switch {
			case errors.Is(rerr, ErrNetlifyNotConnected):
				return mcp.NewToolResultError("netlify not connected — visit /admin/integrations to connect"), nil
			case errors.Is(rerr, ErrGitHubNotConnected):
				return mcp.NewToolResultError("github not connected — visit /admin/integrations to install the Kit GitHub App"), nil
			}
			return mcp.NewToolResultError(rerr.Error()), nil
		}
		msg := fmt.Sprintf(
			"Agent run started.\nrun_id: %s\nstate: %s\nbase branch: %s\npreview URL: %s\n\n"+
				"The preview URL may 404 for the first ~60 seconds while Netlify's build runs.",
			res.RunID, res.State, res.BaseBranch, res.PreviewURL,
		)
		return mcp.NewToolResultText(msg), nil
	})
}
