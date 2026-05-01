package main

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services/messenger"
)

// cardsPushNotifier adapts Messenger into the cards.PushNotifier
// interface. When a card is created with Urgent=true, the cards service
// invokes PushUrgent for each user scope; this adapter looks up the
// target user's slack_user_id and sends a one-line DM with a link back
// to the card. Best-effort by contract — errors are returned to cards
// for logging but never block card creation.
type cardsPushNotifier struct {
	pool      *pgxpool.Pool
	messenger messenger.Messenger
}

func newCardsPushNotifier(pool *pgxpool.Pool, m messenger.Messenger) *cardsPushNotifier {
	return &cardsPushNotifier{pool: pool, messenger: m}
}

// PushUrgent sends a Slack DM to user. Title becomes the bold lead and
// cardURL (if non-empty) is appended as a deep link. The body is
// intentionally not included — a vault failed-unlock body recites the
// failure count which is fine to surface, but other apps may put
// sensitive context in body that doesn't belong in a DM. Title + link
// is the safe minimum.
func (n *cardsPushNotifier) PushUrgent(ctx context.Context, tenantID, userID uuid.UUID, title, _ string, cardURL string) error {
	if n == nil || n.messenger == nil {
		return nil
	}
	user, err := models.GetUserByID(ctx, n.pool, tenantID, userID)
	if err != nil {
		return fmt.Errorf("loading user for push: %w", err)
	}
	if user == nil || user.SlackUserID == "" {
		return nil
	}
	body := "*" + title + "*"
	if cardURL != "" {
		body += "\n<" + cardURL + "|Open in Kit>"
	}
	_, err = n.messenger.Send(ctx, messenger.SendRequest{
		TenantID:  tenantID,
		Channel:   "slack",
		Recipient: messenger.Recipient{SlackUserID: user.SlackUserID},
		UserID:    userID,
		Body:      body,
		Origin:    "cards",
	})
	if err != nil {
		return fmt.Errorf("sending urgent push: %w", err)
	}
	return nil
}
