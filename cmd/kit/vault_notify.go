package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services/messenger"
)

// vaultNotifier implements vault.NotifySurface by dispatching to the
// shared Messenger primitive over Slack DMs. v1's surface map called
// for "briefings" but cards have no per-user scope, so the user-targeted
// security tripwires (failed unlock, reset triggered, access granted)
// land as DMs instead. v2's per-tenant routing config can swap this for
// briefings once cards gain user-scoping.
type vaultNotifier struct {
	pool *pgxpool.Pool
	msg  *messenger.Default
}

func newVaultNotifier(pool *pgxpool.Pool, msg *messenger.Default) *vaultNotifier {
	return &vaultNotifier{pool: pool, msg: msg}
}

func (n *vaultNotifier) NotifyUser(ctx context.Context, tenantID, userID uuid.UUID, body string) error {
	if n == nil || n.msg == nil {
		return nil
	}
	user, err := models.GetUserByID(ctx, n.pool, tenantID, userID)
	if err != nil {
		return fmt.Errorf("looking up user: %w", err)
	}
	if user == nil {
		return errors.New("user not found")
	}
	_, err = n.msg.Send(ctx, messenger.SendRequest{
		TenantID:  tenantID,
		Channel:   "slack",
		Recipient: messenger.Recipient{SlackUserID: user.SlackUserID},
		UserID:    userID,
		Origin:    "vault",
		Body:      body,
	})
	return err
}
