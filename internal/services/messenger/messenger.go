// Package messenger is a channel-agnostic primitive for bot↔user messaging.
//
// Send posts an outbound message via the right channel adapter (Slack today;
// email/SMS later) and records it as an EventTypeMessageSent on the
// session anchoring the conversation. Dispatch routes inbound messages
// (Slack events today; email-poll/SMS-webhook later) back to whichever
// app registered as the reply handler for the originating outbound.
//
// Coordination is the first Phase 1 consumer; the agent loop's
// send_slack_message tool is retrofitted onto Messenger in Phase 2.
package messenger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/models"
)

// Recipient identifies who to send to. Phase 1 supports SlackUserID only;
// Email and Phone are placeholders for later phases.
type Recipient struct {
	SlackUserID string
	Email       string
	Phone       string
}

// SendRequest is the input to Messenger.Send.
type SendRequest struct {
	TenantID  uuid.UUID
	Channel   string // "slack" in Phase 1
	Recipient Recipient
	Body      string

	// Origin identifies the owning app ("coordination", "agent", "email").
	// Used by Dispatch to route subsequent inbound messages back.
	Origin string

	// OriginRef is opaque to messenger; round-tripped to the ReplyHandler.
	// E.g. coordination passes the participant_id.
	OriginRef string

	// AwaitReply registers the resulting session for inbound dispatch.
	// If false, the send is fire-and-forget; replies fall through to the
	// regular agent loop.
	AwaitReply bool

	// UserID is the Kit user we're sending to (resolved from Recipient).
	// Optional; if zero and the recipient is a Slack user already known to
	// Kit, Messenger looks them up.
	UserID uuid.UUID

	// SessionThreadKey overrides the default thread_ts ("") used when
	// resolving the recipient's session. Apps that need their outbound
	// (and the matching inbound) isolated from other bot↔user activity
	// in the same channel set this. Coordination uses
	// "participant:<participant_id>" so each (coord, participant) gets
	// its own session, isolated from other coordinations and from
	// ad-hoc agent chat.
	SessionThreadKey string
}

// SentMessage is the result of a successful Send.
type SentMessage struct {
	SessionID        uuid.UUID
	ChannelMessageID string
}

// InboundEvent is what callers pass to Dispatch. It's a normalized,
// channel-agnostic representation of an inbound message.
type InboundEvent struct {
	TenantID       uuid.UUID
	Channel        string
	SlackChannelID string
	SlackUserID    string
	ThreadTS       string // empty if the inbound is not in a thread
	UserID         uuid.UUID
	Body           string
}

// InboundMessage is what handlers receive.
type InboundMessage struct {
	SessionID uuid.UUID
	Body      string
	Source    InboundEvent
}

// ReplyHandler is the per-app callback invoked by Dispatch when an inbound
// message belongs to a session whose latest awaiting outbound came from
// this app.
//
// Returns (true, nil) if the handler claimed the message (no further
// dispatch). Returns (false, nil) to let inbound fall through to the
// normal agent loop — e.g. the parser determined the message is unrelated
// to the awaiting outbound's purpose.
type ReplyHandler func(ctx context.Context, msg InboundMessage, originRef string) (handled bool, err error)

// Messenger is the public interface.
type Messenger interface {
	Send(ctx context.Context, req SendRequest) (SentMessage, error)
	Dispatch(ctx context.Context, evt InboundEvent) (handled bool, err error)
	RegisterReplyHandler(origin string, handler ReplyHandler)
}

// SlackPoster is the subset of *kitslack.Client that Messenger uses.
// Defined here (not in internal/slack) so tests can substitute a fake.
type SlackPoster interface {
	OpenConversation(ctx context.Context, userID string) (string, error)
	PostMessageReturningTS(ctx context.Context, channel, threadTS, text string) (string, error)
}

// Default is the production Messenger implementation.
type Default struct {
	Pool      *pgxpool.Pool
	Encryptor *crypto.Encryptor

	// SlackClientFor optionally overrides per-tenant Slack client construction.
	// If nil, Default looks up the tenant and decrypts the bot token itself.
	// Tests inject a stub here.
	SlackClientFor func(ctx context.Context, tenantID uuid.UUID) (SlackPoster, error)

	mu       sync.RWMutex
	handlers map[string]ReplyHandler
}

// New constructs a Default Messenger.
func New(pool *pgxpool.Pool, enc *crypto.Encryptor) *Default {
	return &Default{
		Pool:      pool,
		Encryptor: enc,
		handlers:  map[string]ReplyHandler{},
	}
}

// RegisterReplyHandler associates an origin string with a ReplyHandler.
// Apps call this at startup (e.g. coordination registers
// ReplyHandler("coordination", coordHandler)).
//
// Subsequent registrations for the same origin replace the prior handler.
func (m *Default) RegisterReplyHandler(origin string, handler ReplyHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.handlers[origin] = handler
}

func (m *Default) handlerFor(origin string) (ReplyHandler, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	h, ok := m.handlers[origin]
	return h, ok
}

// outboundEventData is the payload for the EventTypeMessageSent event
// recorded by Messenger. Distinct from the existing payload written by
// internal/tools/core.go (which has channel/thread_ts/text/is_dm) because
// Messenger needs the routing fields.
type outboundEventData struct {
	Channel          string `json:"channel"`
	ThreadTS         string `json:"thread_ts"`
	Text             string `json:"text"`
	IsDM             bool   `json:"is_dm"`
	ChannelMessageID string `json:"channel_message_id,omitempty"`

	// Routing metadata used by Dispatch.
	Origin     string `json:"origin,omitempty"`
	OriginRef  string `json:"origin_ref,omitempty"`
	AwaitReply bool   `json:"await_reply,omitempty"`
}

// Send dispatches to the right channel adapter, posts the message,
// records it on the session, and (if AwaitReply) marks the session as
// awaiting a reply for this origin.
func (m *Default) Send(ctx context.Context, req SendRequest) (SentMessage, error) {
	if req.TenantID == uuid.Nil {
		return SentMessage{}, errors.New("messenger.Send: TenantID required")
	}
	if req.Origin == "" {
		return SentMessage{}, errors.New("messenger.Send: Origin required")
	}
	if req.Body == "" {
		return SentMessage{}, errors.New("messenger.Send: Body required")
	}

	switch req.Channel {
	case "slack":
		return m.sendSlack(ctx, req)
	default:
		return SentMessage{}, fmt.Errorf("messenger.Send: unsupported channel %q", req.Channel)
	}
}

// Dispatch attempts to claim an inbound message. Returns (true, nil) if
// a registered handler took it; (false, nil) to fall through to the
// caller's default path (typically the agent loop).
//
// Routing rule: most-recent-bot-outbound-with-await-reply wins. We
// query session_events for the most recent message_sent to this user
// in this channel where await_reply=true. Whatever app sent that
// outbound (via its origin metadata) is the handler we route to. The
// handler runs and the parser/etc. can decide if the inbound is
// actually relevant to that conversation — if not, returns false and
// dispatch falls through to the agent loop.
func (m *Default) Dispatch(ctx context.Context, evt InboundEvent) (bool, error) {
	if evt.TenantID == uuid.Nil {
		return false, errors.New("messenger.Dispatch: TenantID required")
	}
	if evt.SlackChannelID == "" || evt.UserID == uuid.Nil {
		return false, nil
	}

	row := m.Pool.QueryRow(ctx, `
		SELECT s.id, se.data
		FROM session_events se
		JOIN sessions s ON s.id = se.session_id
		WHERE s.tenant_id = $1
		  AND s.slack_channel_id = $2
		  AND s.user_id = $3
		  AND se.event_type = $4
		  AND (se.data->>'await_reply')::boolean = true
		ORDER BY se.created_at DESC
		LIMIT 1
	`, evt.TenantID, evt.SlackChannelID, evt.UserID, string(models.EventTypeMessageSent))

	var sessionID uuid.UUID
	var rawData json.RawMessage
	if err := row.Scan(&sessionID, &rawData); err != nil {
		// pgx.ErrNoRows is the common "no awaiting outbound" path.
		return false, nil //nolint:nilerr // intentional: no claim
	}

	var data outboundEventData
	if err := json.Unmarshal(rawData, &data); err != nil {
		// Old-shape message_sent events (no origin) — fall through.
		return false, nil //nolint:nilerr // intentional: no claim
	}
	if data.Origin == "" {
		return false, nil
	}

	handler, hasHandler := m.handlerFor(data.Origin)
	if !hasHandler {
		return false, nil
	}

	handled, err := handler(ctx, InboundMessage{
		SessionID: sessionID,
		Body:      evt.Body,
		Source:    evt,
	}, data.OriginRef)
	if err != nil {
		return false, fmt.Errorf("reply handler %q: %w", data.Origin, err)
	}

	// Only record the inbound on the routed session if the handler
	// claimed it. If the handler returns false (parser said the message
	// is unrelated), the agent loop runs in its own session and logs
	// the inbound there — we don't want a stale "unrelated" message
	// polluting this conversation's context.
	if handled {
		if err := models.AppendSessionEvent(ctx, m.Pool, evt.TenantID, sessionID, models.EventTypeMessageReceived, map[string]any{
			"text":       evt.Body,
			"channel":    evt.SlackChannelID,
			"origin":     data.Origin,
			"origin_ref": data.OriginRef,
		}); err != nil {
			return true, fmt.Errorf("recording inbound: %w", err)
		}
	}
	return handled, nil
}
