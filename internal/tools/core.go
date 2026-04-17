package tools

import (
	"encoding/json"
	"fmt"

	"github.com/mrdon/kit/internal/models"
)

func registerCoreTools(r *Registry) {
	r.Register(Def{
		Name:        "send_slack_message",
		Description: "Send a message to the user in the current Slack thread. This is the ONLY way to respond to the user. To DM a specific user instead, set user_id to their Slack user ID.",
		Schema: propsReq(map[string]any{
			"text":    field("string", "The message text (Slack mrkdwn)"),
			"user_id": field("string", "Optional: Slack user ID to DM instead of posting to the current channel"),
		}, "text"),
		Terminal: true,
		Handler: func(ec *ExecContext, input json.RawMessage) (string, error) {
			var inp struct {
				Text   string `json:"text"`
				UserID string `json:"user_id"`
			}
			if err := json.Unmarshal(input, &inp); err != nil {
				return "", err
			}
			if inp.Text == "" {
				return "error: text is required", nil
			}

			channel := ec.Channel
			threadTS := ec.ThreadTS
			isDM := false

			if inp.UserID != "" {
				dmChannel, err := ec.Slack.OpenConversation(ec.Ctx, inp.UserID)
				if err != nil {
					return "", fmt.Errorf("opening DM: %w", err)
				}
				channel = dmChannel
				threadTS = ""
				isDM = true
			}

			if err := ec.Slack.PostMessage(ec.Ctx, channel, threadTS, inp.Text); err != nil {
				return "", err
			}
			_ = models.AppendSessionEvent(ec.Ctx, ec.Pool, ec.Tenant.ID, ec.Session.ID, "message_sent", map[string]any{
				"channel":   channel,
				"thread_ts": threadTS,
				"text":      inp.Text,
				"is_dm":     isDM,
			})
			if isDM {
				return "DM sent.", nil
			}
			return "Message sent.", nil
		},
	})
}
