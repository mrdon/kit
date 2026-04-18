package tools

import (
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/mrdon/kit/internal/models"
)

// registerCoreTools wires the three messaging tools plus any other
// core-tool registrations. Splitting the old send_slack_message into
// three kind-specific tools removes the "default target" ambiguity —
// each tool has one target and a clear name, so the model picks the
// right shape from the tool list without needing prompt engineering.
//
// In bot-initiated sessions (scheduled tasks, decision resolves),
// reply_in_thread is omitted. The task's session channel is the author's
// private DM, and silently defaulting to it is almost never what the
// task wants — forcing the agent to pick post_to_channel or dm_user
// makes the target explicit.
func registerCoreTools(r *Registry, botInitiated bool) {
	if !botInitiated {
		r.Register(Def{
			Name:        "reply_in_thread",
			Description: "Reply to the user in the current Slack thread or DM. Use this when the user just said something and you're answering them in place.",
			Schema: propsReq(map[string]any{
				"text": field("string", "The message text (Slack mrkdwn)"),
			}, "text"),
			Terminal: true,
			Handler:  replyInThreadHandler,
		})
	}

	r.Register(Def{
		Name:        "post_to_channel",
		Description: "Post a message to a named Slack channel. Use this when a task or user names a channel (e.g. \"#tmp\", \"the ops channel\"). The channel argument can be a channel name (\"tmp\") or id (\"C123...\"). Do NOT use this to reply to an ongoing conversation.",
		Schema: propsReq(map[string]any{
			"channel": field("string", "Slack channel name or id (e.g. 'tmp' or 'C09ABC')"),
			"text":    field("string", "The message text (Slack mrkdwn)"),
		}, "channel", "text"),
		Terminal: true,
		Handler:  postToChannelHandler,
	})

	r.Register(Def{
		Name:        "dm_user",
		Description: "Send a direct message to a specific user. Use for private, user-directed output (e.g. \"remind me\", \"DM the manager\"). user_id is the Slack user id, not a display name.",
		Schema: propsReq(map[string]any{
			"user_id": field("string", "Slack user id (starts with 'U')"),
			"text":    field("string", "The message text (Slack mrkdwn)"),
		}, "user_id", "text"),
		Terminal: true,
		Handler:  dmUserHandler,
	})
}

func replyInThreadHandler(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	if inp.Text == "" {
		return "error: text is required", nil
	}
	channel := ec.Channel
	threadTS := ec.ThreadTS

	// Session's first top-level post in its own channel: capture the real
	// Slack ts and bind the session to it so later replies route back.
	if threadTS == "" && ec.Session != nil {
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
	logMessageSent(ec, channel, threadTS, inp.Text, false)
	return "Message sent.", nil
}

func postToChannelHandler(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		Channel string `json:"channel"`
		Text    string `json:"text"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	if inp.Channel == "" {
		return "error: channel is required", nil
	}
	if inp.Text == "" {
		return "error: text is required", nil
	}
	if err := ec.Slack.PostMessage(ec.Ctx, inp.Channel, "", inp.Text); err != nil {
		return "", err
	}
	logMessageSent(ec, inp.Channel, "", inp.Text, false)
	return fmt.Sprintf("Message posted to %s.", inp.Channel), nil
}

func dmUserHandler(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		UserID string `json:"user_id"`
		Text   string `json:"text"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	if inp.UserID == "" {
		return "error: user_id is required", nil
	}
	if inp.Text == "" {
		return "error: text is required", nil
	}
	dm, err := ec.Slack.OpenConversation(ec.Ctx, inp.UserID)
	if err != nil {
		return "", fmt.Errorf("opening DM: %w", err)
	}
	if err := ec.Slack.PostMessage(ec.Ctx, dm, "", inp.Text); err != nil {
		return "", err
	}
	logMessageSent(ec, dm, "", inp.Text, true)
	return "DM sent.", nil
}

func logMessageSent(ec *ExecContext, channel, threadTS, text string, isDM bool) {
	_ = models.AppendSessionEvent(ec.Ctx, ec.Pool, ec.Tenant.ID, ec.Session.ID, "message_sent", map[string]any{
		"channel":   channel,
		"thread_ts": threadTS,
		"text":      text,
		"is_dm":     isDM,
	})
}
