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

// SessionResolver lets an app claim ownership of an inbound message's
// session before the default channel-level resolution kicks in. Apps
// that maintain their own per-(activity, recipient) sessions register
// a resolver so messenger routes inbound replies to the right session.
//
// Returns nil session if this resolver doesn't claim the inbound —
// messenger then tries the next resolver, falling back to channel-level.
type SessionResolver func(ctx context.Context, evt InboundEvent) (*models.Session, string, string, error)

// Messenger is the public interface.
type Messenger interface {
	Send(ctx context.Context, req SendRequest) (SentMessage, error)
	Dispatch(ctx context.Context, evt InboundEvent) (handled bool, err error)
	RegisterReplyHandler(origin string, handler ReplyHandler)
	RegisterSessionResolver(resolver SessionResolver)
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

	mu        sync.RWMutex
	handlers  map[string]ReplyHandler
	resolvers []SessionResolver
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

// RegisterSessionResolver appends a session resolver. Resolvers are
// tried in registration order during inbound dispatch.
func (m *Default) RegisterSessionResolver(resolver SessionResolver) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resolvers = append(m.resolvers, resolver)
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
// Inbound resolution: registered SessionResolvers are tried first (an
// app like coordination can claim the inbound based on its own state —
// e.g. "this user has an active participant row"). If no resolver
// claims, fall back to thread/channel session lookup.
func (m *Default) Dispatch(ctx context.Context, evt InboundEvent) (bool, error) {
	if evt.TenantID == uuid.Nil {
		return false, errors.New("messenger.Dispatch: TenantID required")
	}

	var (
		session   *models.Session
		origin    string
		originRef string
	)

	m.mu.RLock()
	resolvers := append([]SessionResolver(nil), m.resolvers...)
	m.mu.RUnlock()

	for _, r := range resolvers {
		s, o, ref, err := r(ctx, evt)
		if err != nil {
			return false, fmt.Errorf("session resolver: %w", err)
		}
		if s != nil {
			session, origin, originRef = s, o, ref
			break
		}
	}

	if session == nil {
		// Fall back to the existing thread/channel-level session (used by
		// the agent loop's own message_sent events).
		s, err := m.resolveSessionForInbound(ctx, evt)
		if err != nil {
			return false, fmt.Errorf("resolving session: %w", err)
		}
		if s == nil {
			return false, nil
		}
		o, ref, ok, err := m.latestAwaitingOrigin(ctx, evt.TenantID, s.ID)
		if err != nil {
			return false, fmt.Errorf("looking up await_reply state: %w", err)
		}
		if !ok {
			return false, nil
		}
		session, origin, originRef = s, o, ref
	}

	handler, hasHandler := m.handlerFor(origin)
	if !hasHandler {
		return false, nil
	}

	handled, err := handler(ctx, InboundMessage{
		SessionID: session.ID,
		Body:      evt.Body,
		Source:    evt,
	}, originRef)
	if err != nil {
		return false, fmt.Errorf("reply handler %q: %w", origin, err)
	}

	// Only record the inbound on the resolved session if the handler
	// claimed it. If the handler returns false, the agent loop will run
	// in a different (channel-level) session and log the inbound there;
	// double-logging confuses subsequent context reconstruction.
	if handled {
		if err := models.AppendSessionEvent(ctx, m.Pool, evt.TenantID, session.ID, models.EventTypeMessageReceived, map[string]any{
			"text":       evt.Body,
			"channel":    evt.SlackChannelID,
			"origin":     origin,
			"origin_ref": originRef,
		}); err != nil {
			return true, fmt.Errorf("recording inbound: %w", err)
		}
	}
	return handled, nil
}

// resolveSessionForInbound looks up a session for an inbound message.
// Tries (channel, thread_ts) match first if thread_ts is set; falls back
// to (channel, "") match for un-threaded DMs. Returns nil session if no
// match — the inbound should fall through to the regular agent path.
//
//nolint:nilnil // (nil, nil) is the intentional "no match" signal here.
func (m *Default) resolveSessionForInbound(ctx context.Context, evt InboundEvent) (*models.Session, error) {
	if evt.SlackChannelID == "" {
		return nil, nil
	}
	// Thread-replies and channel-level messages key by thread_ts; for
	// un-threaded DMs, thread_ts is "".
	session, err := models.FindSessionByThread(ctx, m.Pool, evt.TenantID, evt.SlackChannelID, evt.ThreadTS)
	if err != nil {
		return nil, err
	}
	return session, nil
}

// latestAwaitingOrigin finds the most recent EventTypeMessageSent on the
// session whose data has await_reply=true. Returns the origin and
// origin_ref, or ok=false if no such event exists.
func (m *Default) latestAwaitingOrigin(ctx context.Context, tenantID, sessionID uuid.UUID) (origin, originRef string, ok bool, err error) {
	row := m.Pool.QueryRow(ctx, `
		SELECT data
		FROM session_events
		WHERE tenant_id = $1 AND session_id = $2 AND event_type = $3
		ORDER BY created_at DESC
		LIMIT 1
	`, tenantID, sessionID, string(models.EventTypeMessageSent))
	var raw json.RawMessage
	if err := row.Scan(&raw); err != nil {
		// pgx returns ErrNoRows when there's no message_sent on this session,
		// which means nothing is awaiting reply.
		return "", "", false, nil
	}
	var data outboundEventData
	if err := json.Unmarshal(raw, &data); err != nil {
		// Older message_sent events (written by tools/core.go) have a
		// different payload shape — they unmarshal cleanly into
		// outboundEventData with empty Origin. Treat as not-awaiting.
		return "", "", false, nil
	}
	if !data.AwaitReply || data.Origin == "" {
		return "", "", false, nil
	}
	return data.Origin, data.OriginRef, true, nil
}
