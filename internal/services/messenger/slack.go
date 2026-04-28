package messenger

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

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

	// Top-level DM (no thread).
	ts, err := client.PostMessageReturningTS(ctx, imChannel, "", req.Body)
	if err != nil {
		return SentMessage{}, fmt.Errorf("posting DM: %w", err)
	}

	if err := models.AppendSessionEvent(ctx, m.Pool, req.TenantID, session.ID, models.EventTypeMessageSent, outboundEventData{
		Channel:          imChannel,
		ThreadTS:         "",
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
