package tools

import (
	"encoding/json"

	"github.com/mrdon/kit/internal/models"
)

func registerCoreTools(r *Registry) {
	r.Register(Def{
		Name:        "send_slack_message",
		Description: "Send a message to the user in the current Slack thread. This is the ONLY way to respond to the user.",
		Schema:      propsReq(map[string]any{"text": field("string", "The message text (Slack mrkdwn)")}, "text"),
		Terminal:    true,
		Handler: func(ec *ExecContext, input json.RawMessage) (string, error) {
			var inp struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(input, &inp); err != nil {
				return "", err
			}
			if inp.Text == "" {
				return "error: text is required", nil
			}
			if err := ec.Slack.PostMessage(ec.Ctx, ec.Channel, ec.ThreadTS, inp.Text); err != nil {
				return "", err
			}
			_ = models.AppendSessionEvent(ec.Ctx, ec.Pool, ec.Tenant.ID, ec.Session.ID, "message_sent", map[string]any{
				"channel":   ec.Channel,
				"thread_ts": ec.ThreadTS,
				"text":      inp.Text,
			})
			return "Message sent.", nil
		},
	})
}
