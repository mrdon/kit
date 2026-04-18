package tools

import (
	"context"
	"log/slog"

	"github.com/mrdon/kit/internal/models"
)

// Responder is the destination for the agent's terminal reply to the
// user. reply_in_thread delegates its side effect here so the same tool
// can post to Slack in a live conversation or stream over SSE in the web
// chat path.
type Responder interface {
	Send(ctx context.Context, text string) error
}

// SlackResponder posts to Slack, binding the session to the Slack thread
// on the first reply when the session doesn't yet know its thread_ts
// (e.g. a scheduled task's first post). Mutates ec.ThreadTS and the
// session row so later replies route back.
type SlackResponder struct {
	ec *ExecContext
}

// NewSlackResponder is the default responder for Slack-initiated and
// task-initiated agent runs.
func NewSlackResponder(ec *ExecContext) *SlackResponder {
	return &SlackResponder{ec: ec}
}

// Send posts text to the Slack channel/thread on ec. On the first reply
// of a session that hasn't been bound to a Slack thread yet, it captures
// the real thread ts and rewrites both ec.ThreadTS and the session row.
func (r *SlackResponder) Send(ctx context.Context, text string) error {
	ec := r.ec
	if ec.ThreadTS == "" && ec.Session != nil {
		ts, err := ec.Slack.PostMessageReturningTS(ctx, ec.Channel, "", text)
		if err != nil {
			return err
		}
		if ts != "" {
			if err := models.UpdateSessionThreadTS(ctx, ec.Pool, ec.Tenant.ID, ec.Session.ID, ts); err != nil {
				slog.Warn("binding session to slack thread", "session_id", ec.Session.ID, "error", err)
			} else {
				ec.Session.SlackThreadTS = ts
				ec.ThreadTS = ts
			}
		}
		return nil
	}
	return ec.Slack.PostMessage(ctx, ec.Channel, ec.ThreadTS, text)
}

// FuncResponder adapts a plain function to the Responder interface. Use
// for the chat SSE path where Send just emits an event.
type FuncResponder func(ctx context.Context, text string) error

// Send satisfies Responder.
func (f FuncResponder) Send(ctx context.Context, text string) error {
	return f(ctx, text)
}
