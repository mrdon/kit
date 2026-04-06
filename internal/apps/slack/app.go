package slack

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

func init() {
	apps.Register(&SlackApp{})
}

// SlackApp provides Slack channel message search.
type SlackApp struct {
	svc *SlackChannelService
}

// Init sets up the service after DB is available.
func (a *SlackApp) Init(pool *pgxpool.Pool) {
	a.svc = &SlackChannelService{pool: pool}
}

func (a *SlackApp) Name() string { return "slack" }

func (a *SlackApp) SystemPrompt() string {
	return `## Slack Channel Search
You can search and browse messages in configured Slack channels. Use list_slack_channels to see which channels are available, then get_slack_messages to read or search messages. You can filter by keyword, date, and page through results. When asked to find information in Slack (e.g. todos, action items, decisions), search relevant channels and analyze the messages.`
}

func (a *SlackApp) ToolMetas() []services.ToolMeta {
	return slackTools
}

func (a *SlackApp) RegisterAgentTools(registerer any, isAdmin bool) {
	r := registerer.(*tools.Registry)
	registerSlackAgentTools(r, isAdmin, a.svc)
}

func (a *SlackApp) RegisterMCPTools(pool *pgxpool.Pool, svc *services.Services, caller *services.Caller) []mcpserver.ServerTool {
	return buildSlackMCPTools(pool, svc, a.svc, caller)
}

func (a *SlackApp) RegisterRoutes(_ *http.ServeMux) {}

func (a *SlackApp) CronJobs() []apps.CronJob { return nil }

var slackTools = []services.ToolMeta{
	{
		Name:        "configure_slack_channel",
		Description: "Add a Slack channel for message search. The bot must already be invited to the channel. Specify role_scopes to restrict access.",
		AdminOnly:   true,
		Schema: services.PropsReq(map[string]any{
			"channel_id":  services.Field("string", "Slack channel ID (e.g. C1234567890)"),
			"role_scopes": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Role names that can access this channel. Empty = all users in tenant."},
		}, "channel_id"),
	},
	{
		Name:        "list_slack_channels",
		Description: "List Slack channels configured for message search that you have access to.",
		Schema:      services.Props(map[string]any{}),
	},
	{
		Name:        "get_slack_messages",
		Description: "Get messages from a configured Slack channel. Supports keyword filtering, date range, and cursor-based paging.",
		Schema: services.PropsReq(map[string]any{
			"channel_id": services.Field("string", "Slack channel ID to read from"),
			"query":      services.Field("string", "Optional keyword to filter messages (case-insensitive)"),
			"after":      services.Field("string", "Only messages after this date (YYYY-MM-DD). Defaults to last 24 hours."),
			"cursor":     services.Field("string", "Pagination cursor from a previous get_slack_messages result"),
		}, "channel_id"),
	},
}
