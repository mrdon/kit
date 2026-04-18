package tools

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/mrdon/kit/internal/models"
)

func registerCoreTools(r *Registry) {
	r.Register(Def{
		Name:        "send_slack_message",
		Description: "Send a message to the user in the current Slack thread. This is the ONLY way to respond to the user. To DM a specific user, set user_id. To post to a different channel, set channel.",
		Schema: propsReq(map[string]any{
			"text":    field("string", "The message text (Slack mrkdwn)"),
			"user_id": field("string", "Optional: Slack user ID to DM instead of posting to the current channel"),
			"channel": field("string", "Optional: Slack channel ID to post to instead of the current channel"),
		}, "text"),
		Terminal: true,
		Handler: func(ec *ExecContext, input json.RawMessage) (string, error) {
			var inp struct {
				Text    string `json:"text"`
				UserID  string `json:"user_id"`
				Channel string `json:"channel"`
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
			} else if inp.Channel != "" && inp.Channel != ec.Channel {
				channel = inp.Channel
				threadTS = ""
			}

			// If this is the session's first top-level post in its own channel,
			// capture the real Slack ts and bind the session to it so replies
			// in the resulting thread route back to this session.
			bindSession := !isDM && channel == ec.Channel && threadTS == "" && ec.Session != nil
			if bindSession {
				ts, err := ec.Slack.PostMessageReturningTS(ec.Ctx, channel, "", inp.Text)
				if err != nil {
					return "", err
				}
				if ts != "" {
					if err := models.UpdateSessionThreadTS(ec.Ctx, ec.Pool, ec.Tenant.ID, ec.Session.ID, ts); err != nil {
						slog.Warn("binding session to slack thread", "session_id", ec.Session.ID, "error", err)
					} else {
						ec.Session.SlackThreadTS = ts
						ec.ThreadTS = ts
						threadTS = ts
					}
				}
			} else {
				if err := ec.Slack.PostMessage(ec.Ctx, channel, threadTS, inp.Text); err != nil {
					return "", err
				}
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
