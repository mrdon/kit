package slack

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/models"
)

// TenantClient builds a client with the workspace's own bot token, for an app
// that posts to a channel an admin picked rather than replying to a message.
func TenantClient(ctx context.Context, pool *pgxpool.Pool, enc *crypto.Encryptor, tenantID uuid.UUID) (*Client, error) {
	if enc == nil {
		return nil, errors.New("slack is not configured on this deployment")
	}
	tenant, err := models.GetTenantByID(ctx, pool, tenantID)
	if err != nil {
		return nil, fmt.Errorf("loading tenant: %w", err)
	}
	if tenant == nil {
		return nil, fmt.Errorf("tenant %s not found", tenantID)
	}
	botToken, err := enc.Decrypt(tenant.BotToken)
	if err != nil {
		return nil, fmt.Errorf("decrypting bot token: %w", err)
	}
	return NewClient(botToken), nil
}
