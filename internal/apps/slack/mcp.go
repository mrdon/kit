package slack

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/mcpauth"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	kitslack "github.com/mrdon/kit/internal/slack"
)

func buildSlackMCPTools(pool *pgxpool.Pool, svc *services.Services, chanSvc *SlackChannelService) []mcpserver.ServerTool {
	var result []mcpserver.ServerTool
	for _, meta := range slackTools {
		handler := slackMCPHandler(meta.Name, pool, svc, chanSvc)
		if handler == nil {
			continue
		}
		result = append(result, apps.MCPToolFromMeta(meta, handler))
	}
	return result
}

func slackMCPHandler(name string, pool *pgxpool.Pool, svc *services.Services, chanSvc *SlackChannelService) mcpserver.ToolHandlerFunc {
	switch name {
	case "configure_slack_channel":
		return mcpConfigureChannel(pool, svc, chanSvc)
	case "list_slack_channels":
		return mcpListChannels(chanSvc)
	case "get_slack_messages":
		return mcpGetMessages(pool, svc, chanSvc)
	default:
		return nil
	}
}

// makeSlackClient creates a Slack client for the caller's tenant by decrypting the bot token.
func makeSlackClient(ctx context.Context, pool *pgxpool.Pool, svc *services.Services, caller *services.Caller) (*kitslack.Client, error) {
	if svc.Enc == nil {
		return nil, errors.New("encryptor not available")
	}
	tenant, err := models.GetTenantByID(ctx, pool, caller.TenantID)
	if err != nil {
		return nil, fmt.Errorf("getting tenant: %w", err)
	}
	token, err := svc.Enc.Decrypt(tenant.BotToken)
	if err != nil {
		return nil, fmt.Errorf("decrypting bot token: %w", err)
	}
	return kitslack.NewClient(token), nil
}

func mcpConfigureChannel(pool *pgxpool.Pool, svc *services.Services, chanSvc *SlackChannelService) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		channelID, _ := req.RequireString("channel_id")
		channelName := req.GetString("channel_name", "")
		args := req.GetArguments()

		var roleScopes []string
		if rs, ok := args["role_scopes"].([]any); ok {
			for _, r := range rs {
				if s, ok := r.(string); ok {
					roleScopes = append(roleScopes, s)
				}
			}
		}

		sc, err := makeSlackClient(ctx, pool, svc, caller)
		if err != nil {
			slog.Error("creating slack client for MCP", "error", err)
			return mcp.NewToolResultError("Failed to create Slack client."), nil
		}

		ch, err := chanSvc.Configure(ctx, caller, sc, channelID, channelName, roleScopes)
		if err != nil {
			if errors.Is(err, services.ErrForbidden) {
				return mcp.NewToolResultError("Permission denied."), nil
			}
			return mcp.NewToolResultError(err.Error()), nil
		}

		scope := "all users"
		if len(roleScopes) > 0 {
			scope = "roles: " + strings.Join(roleScopes, ", ")
		}
		return mcp.NewToolResultText(fmt.Sprintf("Configured channel #%s (%s). Access: %s.", ch.ChannelName, ch.SlackChannelID, scope)), nil
	})
}

func mcpListChannels(chanSvc *SlackChannelService) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, _ mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		channels, err := chanSvc.List(ctx, caller)
		if err != nil {
			return nil, err
		}
		if len(channels) == 0 {
			return mcp.NewToolResultText("No Slack channels configured for search."), nil
		}
		return mcp.NewToolResultText(formatChannelList(channels)), nil
	})
}

func mcpGetMessages(pool *pgxpool.Pool, svc *services.Services, chanSvc *SlackChannelService) mcpserver.ToolHandlerFunc {
	return mcpauth.WithCaller(func(ctx context.Context, req mcp.CallToolRequest, caller *services.Caller) (*mcp.CallToolResult, error) {
		channelID, _ := req.RequireString("channel_id")

		sc, err := makeSlackClient(ctx, pool, svc, caller)
		if err != nil {
			slog.Error("creating slack client for MCP", "error", err)
			return mcp.NewToolResultError("Failed to create Slack client."), nil
		}

		result, err := chanSvc.GetMessages(ctx, caller, sc, GetMessagesOpts{
			ChannelID: channelID,
			Query:     req.GetString("query", ""),
			After:     req.GetString("after", ""),
			Cursor:    req.GetString("cursor", ""),
		})
		if err != nil {
			if errors.Is(err, services.ErrForbidden) {
				return mcp.NewToolResultError("You don't have access to this channel."), nil
			}
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(formatMessages(result)), nil
	})
}
