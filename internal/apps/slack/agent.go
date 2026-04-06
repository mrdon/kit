package slack

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mrdon/kit/internal/services"
	kitslack "github.com/mrdon/kit/internal/slack"
	"github.com/mrdon/kit/internal/tools"
)

func registerSlackAgentTools(r *tools.Registry, isAdmin bool, svc *SlackChannelService) {
	for _, meta := range slackTools {
		if meta.AdminOnly && !isAdmin {
			continue
		}
		r.Register(tools.Def{
			Name:        meta.Name,
			Description: meta.Description,
			Schema:      meta.Schema,
			AdminOnly:   meta.AdminOnly,
			Handler:     slackAgentHandler(meta.Name, svc),
		})
	}
}

func slackAgentHandler(name string, svc *SlackChannelService) tools.HandlerFunc {
	switch name {
	case "configure_slack_channel":
		return handleConfigureChannel(svc)
	case "list_slack_channels":
		return handleListChannels(svc)
	case "get_slack_messages":
		return handleGetMessages(svc)
	default:
		return func(_ *tools.ExecContext, _ json.RawMessage) (string, error) {
			return "", fmt.Errorf("unknown slack tool: %s", name)
		}
	}
}

func handleConfigureChannel(svc *SlackChannelService) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			ChannelID  string   `json:"channel_id"`
			RoleScopes []string `json:"role_scopes"`
		}
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}

		caller := ec.Caller()
		ch, err := svc.Configure(ec.Ctx, caller, ec.Slack, inp.ChannelID, inp.RoleScopes)
		if err != nil {
			if errors.Is(err, services.ErrForbidden) {
				return "Only admins can configure Slack channels.", nil
			}
			return "Error: " + err.Error(), nil
		}

		scope := "all users"
		if len(inp.RoleScopes) > 0 {
			scope = "roles: " + strings.Join(inp.RoleScopes, ", ")
		}
		return fmt.Sprintf("Configured channel #%s (%s) for message search. Access: %s.", ch.ChannelName, ch.SlackChannelID, scope), nil
	}
}

func handleListChannels(svc *SlackChannelService) tools.HandlerFunc {
	return func(ec *tools.ExecContext, _ json.RawMessage) (string, error) {
		caller := ec.Caller()
		channels, err := svc.List(ec.Ctx, caller)
		if err != nil {
			return "", fmt.Errorf("listing channels: %w", err)
		}
		if len(channels) == 0 {
			return "No Slack channels configured for search.", nil
		}
		return formatChannelList(channels), nil
	}
}

func handleGetMessages(svc *SlackChannelService) tools.HandlerFunc {
	return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
		var inp struct {
			ChannelID string `json:"channel_id"`
			Query     string `json:"query"`
			After     string `json:"after"`
			Cursor    string `json:"cursor"`
		}
		if err := json.Unmarshal(input, &inp); err != nil {
			return "", fmt.Errorf("parsing input: %w", err)
		}

		caller := ec.Caller()
		result, err := svc.GetMessages(ec.Ctx, caller, ec.Slack, GetMessagesOpts{
			ChannelID: inp.ChannelID,
			Query:     inp.Query,
			After:     inp.After,
			Cursor:    inp.Cursor,
		})
		if err != nil {
			if errors.Is(err, services.ErrForbidden) {
				return "You don't have access to this channel.", nil
			}
			return "Error: " + err.Error(), nil
		}

		return formatMessages(result), nil
	}
}

func formatChannelList(channels []SlackChannel) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Found %d channel(s):\n", len(channels))
	for _, ch := range channels {
		fmt.Fprintf(&b, "  #%s (%s)\n", ch.ChannelName, ch.SlackChannelID)
	}
	return b.String()
}

func formatMessages(result *GetMessagesResult) string {
	if len(result.Messages) == 0 {
		return "No messages found."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d message(s):\n\n", len(result.Messages))
	for _, m := range result.Messages {
		fmt.Fprintf(&b, "[%s] %s: %s\n", formatTS(m.Timestamp), m.UserID, m.Text)
	}
	if result.HasMore && result.NextCursor != "" {
		fmt.Fprintf(&b, "\nMore messages available. Use cursor: %s", result.NextCursor)
	}
	return b.String()
}

func formatTS(ts string) string {
	// Slack timestamps are Unix.microseconds — parse the integer part
	parts := strings.SplitN(ts, ".", 2)
	if len(parts) == 0 {
		return ts
	}
	var sec int64
	for _, c := range parts[0] {
		if c < '0' || c > '9' {
			return ts
		}
		sec = sec*10 + int64(c-'0')
	}
	return kitslack.FormatTimestamp(sec)
}
