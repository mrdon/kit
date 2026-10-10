package chat

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	kitslack "github.com/mrdon/kit/internal/slack"
)

// ResolveContext loads what Execute needs for a signed-in caller: the
// tenant, the user, and a Slack client built from the tenant's bot token.
// Every web chat surface (cards, console quick chat, poster chat) goes
// through it.
func ResolveContext(ctx context.Context, pool *pgxpool.Pool, enc *crypto.Encryptor, caller *services.Caller) (*models.Tenant, *models.User, *kitslack.Client, error) {
	tenant, err := models.GetTenantByID(ctx, pool, caller.TenantID)
	if err != nil {
		return nil, nil, nil, err
	}
	if tenant == nil {
		return nil, nil, nil, errors.New("tenant not found")
	}
	user, err := models.GetUserByID(ctx, pool, tenant.ID, caller.UserID)
	if err != nil {
		return nil, nil, nil, err
	}
	if user == nil {
		return nil, nil, nil, errors.New("user not found")
	}
	botToken, err := enc.Decrypt(tenant.BotToken)
	if err != nil {
		return nil, nil, nil, err
	}
	return tenant, user, kitslack.NewClient(botToken), nil
}
