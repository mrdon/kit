package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/tools/approval"
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
			// Live in-session reply: the channel is session-bound, gating
			// breaks the conversational loop. The agent has other tools for
			// deliberate outbound sends.
			DenyCallerGate: true,
			Handler:        replyInThreadHandler,
		})
	}

	r.Register(Def{
		Name:        "post_to_channel",
		Description: "Post a message to a named Slack channel. Use this when a task or user names a channel (e.g. \"#tmp\", \"the ops channel\"). The channel argument can be a channel name (\"tmp\") or id (\"C123...\"). Do NOT use this to reply to an ongoing conversation.",
		Schema: propsReq(map[string]any{
			"channel": field("string", "Slack channel name or id (e.g. 'tmp' or 'C09ABC')"),
			"text":    field("string", "The message text (Slack mrkdwn)"),
		}, "channel", "text"),
		Terminal:        true,
		GateCardPreview: postToChannelGatePreview,
		Handler:         postToChannelHandler,
	})

	r.Register(Def{
		Name:        "dm_user",
		Description: "Send a direct message to a specific user. Use for private, user-directed output (e.g. \"remind me\", \"DM the manager\"). user_id is the Slack user id, not a display name.",
		Schema: propsReq(map[string]any{
			"user_id": field("string", "Slack user id (starts with 'U')"),
			"text":    field("string", "The message text (Slack mrkdwn)"),
		}, "user_id", "text"),
		Terminal:        true,
		GateCardPreview: dmUserGatePreview,
		Handler:         dmUserHandler,
	})
}

func postToChannelGatePreview(input json.RawMessage) GateCardPreview {
	var args struct {
		Channel string `json:"channel"`
	}
	_ = json.Unmarshal(input, &args)
	title := "Post to Slack channel?"
	if args.Channel != "" {
		title = "Post to " + displayChannel(args.Channel) + "?"
	}
	return GateCardPreview{
		Title:        title,
		ApproveLabel: "Post",
		SkipLabel:    "Don't post",
	}
}

func dmUserGatePreview(input json.RawMessage) GateCardPreview {
	var args struct {
		UserID string `json:"user_id"`
	}
	_ = json.Unmarshal(input, &args)
	title := "Send DM?"
	if args.UserID != "" {
		title = "Send DM to <@" + args.UserID + ">?"
	}
	return GateCardPreview{
		Title:        title,
		ApproveLabel: "Send DM",
		SkipLabel:    "Don't send",
	}
}

// displayChannel prefixes a bare channel name with '#'. Leaves Slack
// channel ids (start with C/G/D) alone since the PWA can render those
// as-is and the agent can pass either shape.
func displayChannel(c string) string {
	if c == "" {
		return c
	}
	if c[0] == '#' {
		return c
	}
	if c[0] == 'C' || c[0] == 'G' || c[0] == 'D' {
		return c
	}
	return "#" + c
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
	responder := ec.Responder
	if responder == nil {
		responder = NewSlackResponder(ec)
	}
	if err := responder.Send(ec.Ctx, inp.Text); err != nil {
		return "", err
	}
	logMessageSent(ec, ec.Channel, ec.ThreadTS, inp.Text, false)
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
	// If the call reached us via the approval path, dedupe on the
	// resolve token so a stuck-resolving sweep retry doesn't double-post.
	// Direct (un-approved) calls just post — there's no retry machinery
	// to defend against and idempotency costs a round-trip we don't need.
	if _, resolveTok, ok := approval.FromCtx(ec.Ctx); ok && resolveTok != [16]byte{} {
		caller := ec.Caller()
		_, err := slackSendOnce(ec.Ctx, ec.Pool, resolveTok, caller.TenantID, caller.UserID,
			"post_to_channel", inp.Channel,
			func(ctx context.Context) (string, error) {
				return ec.Slack.PostMessageReturningTS(ctx, inp.Channel, "", inp.Text)
			})
		if err != nil {
			return "", err
		}
		logMessageSent(ec, inp.Channel, "", inp.Text, false)
		return fmt.Sprintf("Message posted to %s.", inp.Channel), nil
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
	if _, resolveTok, ok := approval.FromCtx(ec.Ctx); ok && resolveTok != [16]byte{} {
		caller := ec.Caller()
		_, err := slackSendOnce(ec.Ctx, ec.Pool, resolveTok, caller.TenantID, caller.UserID,
			"dm_user", dm,
			func(ctx context.Context) (string, error) {
				return ec.Slack.PostMessageReturningTS(ctx, dm, "", inp.Text)
			})
		if err != nil {
			return "", err
		}
		logMessageSent(ec, dm, "", inp.Text, true)
		return "DM sent.", nil
	}
	if err := ec.Slack.PostMessage(ec.Ctx, dm, "", inp.Text); err != nil {
		return "", err
	}
	logMessageSent(ec, dm, "", inp.Text, true)
	return "DM sent.", nil
}

func logMessageSent(ec *ExecContext, channel, threadTS, text string, isDM bool) {
	_ = models.AppendSessionEvent(ec.Ctx, ec.Pool, ec.Tenant.ID, ec.Session.ID, models.EventTypeMessageSent, map[string]any{
		"channel":   channel,
		"thread_ts": threadTS,
		"text":      text,
		"is_dm":     isDM,
	})
}
