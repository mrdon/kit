package messenger

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mrdon/kit/internal/models"
	kitslack "github.com/mrdon/kit/internal/slack"
)

// sendSlack handles req.Channel == "slack". Opens (or reuses) the IM
// channel between the bot and the recipient, posts a top-level DM,
// records the message_sent event with routing metadata.
//
// Phase 1 supports DMs only. If the caller wants to post into an existing
// thread (e.g. agent loop replying to a user-initiated thread), the
// retrofit in Phase 2 will extend SendRequest with a ThreadTS field.
func (m *Default) sendSlack(ctx context.Context, req SendRequest) (SentMessage, error) {
	if req.Recipient.SlackUserID == "" {
		return SentMessage{}, errors.New("messenger.sendSlack: Recipient.SlackUserID required")
	}

	client, err := m.slackClient(ctx, req.TenantID)
	if err != nil {
		return SentMessage{}, fmt.Errorf("getting slack client: %w", err)
	}

	imChannel, err := client.OpenConversation(ctx, req.Recipient.SlackUserID)
	if err != nil {
		return SentMessage{}, fmt.Errorf("opening DM channel: %w", err)
	}

	userID := req.UserID
	if userID == uuid.Nil {
		// Look up Kit user by Slack ID; nullable on session if not found
		// (external users in later phases).
		u, err := models.GetUserBySlackID(ctx, m.Pool, req.TenantID, req.Recipient.SlackUserID)
		if err == nil && u != nil {
			userID = u.ID
		}
	}

	// Resolve or create the session. Default key is "" (channel-level),
	// but apps can override via SessionThreadKey to isolate their flow
	// from other bot↔user activity in the same channel. Coordination
	// uses "participant:<id>" so each (coord, participant) gets its own
	// session.
	threadKey := req.SessionThreadKey
	session, err := models.GetOrCreateSession(ctx, m.Pool, req.TenantID, imChannel, threadKey, userID)
	if err != nil {
		return SentMessage{}, fmt.Errorf("resolving session: %w", err)
	}

	// Slack thread anchor: thread under the FIRST outbound on this
	// session so a single conversation reads in context. When the caller
	// supplies (Origin, OriginRef) — e.g. coord notifications routed to
	// the organizer's main session — narrow the anchor to outbounds
	// matching that origin so each coord gets its own thread instead of
	// burying the new message under unrelated history.
	//
	// Sessions that opt into isolation via SessionThreadKey already have
	// every outbound in their flow on its own session — Slack threading
	// on top of that buries follow-ups in a thread sidebar that
	// recipients often miss in 1:1 DMs. Skip threading for those so the
	// conversation reads as a normal back-and-forth DM.
	threadTS := ""
	if req.SessionThreadKey == "" {
		threadTS, err = earliestOutboundTS(ctx, m.Pool, session.ID, req.Origin, req.OriginRef)
		if err != nil {
			return SentMessage{}, fmt.Errorf("looking up thread anchor: %w", err)
		}
	}

	ts, err := client.PostMessageReturningTS(ctx, imChannel, threadTS, req.Body)
	if err != nil {
		return SentMessage{}, fmt.Errorf("posting DM: %w", err)
	}

	if err := models.AppendSessionEvent(ctx, m.Pool, req.TenantID, session.ID, models.EventTypeMessageSent, outboundEventData{
		Channel:          imChannel,
		ThreadTS:         threadTS,
		Text:             req.Body,
		IsDM:             true,
		ChannelMessageID: ts,
		Origin:           req.Origin,
		OriginRef:        req.OriginRef,
		AwaitReply:       req.AwaitReply,
	}); err != nil {
		return SentMessage{}, fmt.Errorf("recording outbound: %w", err)
	}

	return SentMessage{
		SessionID:        session.ID,
		ChannelMessageID: ts,
	}, nil
}

// earliestOutboundTS returns the channel_message_id of the OLDEST
// message_sent event on this session, used as the Slack thread anchor
// for subsequent outbounds. Returns "" if no prior outbound — meaning
// the next send goes top-level.
//
// When origin and originRef are both non-empty, the search is narrowed
// to outbounds carrying the same routing tag. Callers that share a
// session across multiple flows (e.g. coord notifications living inside
// the organizer's main bot session) use this to keep each flow's
// messages threaded together instead of all bunched under the first
// outbound the session ever saw.
func earliestOutboundTS(ctx context.Context, pool poolLike, sessionID uuid.UUID, origin, originRef string) (string, error) {
	var row pgx.Row
	if origin != "" && originRef != "" {
		row = pool.QueryRow(ctx, `
			SELECT data->>'channel_message_id'
			FROM session_events
			WHERE session_id = $1 AND event_type = $2
			  AND data->>'origin' = $3 AND data->>'origin_ref' = $4
			ORDER BY created_at ASC
			LIMIT 1
		`, sessionID, string(models.EventTypeMessageSent), origin, originRef)
	} else {
		row = pool.QueryRow(ctx, `
			SELECT data->>'channel_message_id'
			FROM session_events
			WHERE session_id = $1 AND event_type = $2
			ORDER BY created_at ASC
			LIMIT 1
		`, sessionID, string(models.EventTypeMessageSent))
	}
	var ts *string
	if err := row.Scan(&ts); err != nil {
		return "", nil //nolint:nilerr // pgx.ErrNoRows means no anchor
	}
	if ts == nil {
		return "", nil
	}
	return *ts, nil
}

// poolLike is the subset of pgxpool.Pool used by earliestOutboundTS,
// kept narrow so the helper stays testable.
type poolLike interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// slackClient returns a SlackPoster for the tenant. Uses the injected
// SlackClientFor if set (tests), otherwise looks up the tenant and
// decrypts the bot token.
func (m *Default) slackClient(ctx context.Context, tenantID uuid.UUID) (SlackPoster, error) {
	if m.SlackClientFor != nil {
		return m.SlackClientFor(ctx, tenantID)
	}
	tenant, err := models.GetTenantByID(ctx, m.Pool, tenantID)
	if err != nil {
		return nil, fmt.Errorf("loading tenant: %w", err)
	}
	if tenant == nil {
		return nil, fmt.Errorf("tenant %s not found", tenantID)
	}
	botToken, err := m.Encryptor.Decrypt(tenant.BotToken)
	if err != nil {
		return nil, fmt.Errorf("decrypting bot token: %w", err)
	}
	return kitslack.NewClient(botToken), nil
}
