package models

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Tenant struct {
	ID            uuid.UUID
	SlackTeamID   string
	Name          string
	BotToken      string // encrypted
	BusinessType  *string
	Timezone      string
	SetupComplete bool
	CreatedAt     time.Time
}

// UpsertTenant creates or updates a tenant by slack_team_id.
// On re-install, updates the bot token and name.
func UpsertTenant(ctx context.Context, pool *pgxpool.Pool, slackTeamID, name, encryptedToken string) (*Tenant, error) {
	tenant := &Tenant{}
	err := pool.QueryRow(ctx, `
		INSERT INTO tenants (id, slack_team_id, name, bot_token)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (slack_team_id)
		DO UPDATE SET name = EXCLUDED.name, bot_token = EXCLUDED.bot_token
		RETURNING id, slack_team_id, name, bot_token, business_type, timezone, setup_complete, created_at
	`, uuid.New(), slackTeamID, name, encryptedToken).Scan(
		&tenant.ID, &tenant.SlackTeamID, &tenant.Name, &tenant.BotToken,
		&tenant.BusinessType, &tenant.Timezone, &tenant.SetupComplete, &tenant.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("upserting tenant: %w", err)
	}
	return tenant, nil
}

// GetTenantBySlackTeamID finds a tenant by its Slack team ID.
func GetTenantBySlackTeamID(ctx context.Context, pool *pgxpool.Pool, slackTeamID string) (*Tenant, error) {
	tenant := &Tenant{}
	err := pool.QueryRow(ctx, `
		SELECT id, slack_team_id, name, bot_token, business_type, timezone, setup_complete, created_at
		FROM tenants WHERE slack_team_id = $1
	`, slackTeamID).Scan(
		&tenant.ID, &tenant.SlackTeamID, &tenant.Name, &tenant.BotToken,
		&tenant.BusinessType, &tenant.Timezone, &tenant.SetupComplete, &tenant.CreatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getting tenant: %w", err)
	}
	return tenant, nil
}

// UpdateTenantSetup marks a tenant as setup complete and sets business info.
func UpdateTenantSetup(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, businessType, timezone string) error {
	_, err := pool.Exec(ctx, `
		UPDATE tenants SET business_type = $2, timezone = $3, setup_complete = true
		WHERE id = $1
	`, tenantID, businessType, timezone)
	if err != nil {
		return fmt.Errorf("updating tenant setup: %w", err)
	}
	return nil
}
