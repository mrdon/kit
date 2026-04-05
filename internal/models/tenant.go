package models

import (
	"context"
	"errors"
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
	DefaultRoleID *uuid.UUID
	CreatedAt     time.Time
}

func UpsertTenant(ctx context.Context, pool *pgxpool.Pool, slackTeamID, name, encryptedToken string) (*Tenant, error) {
	tenant := &Tenant{}
	err := pool.QueryRow(ctx, `
		INSERT INTO tenants (id, slack_team_id, name, bot_token)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (slack_team_id)
		DO UPDATE SET name = EXCLUDED.name, bot_token = EXCLUDED.bot_token
		RETURNING id, slack_team_id, name, bot_token, business_type, timezone, setup_complete, default_role_id, created_at
	`, uuid.New(), slackTeamID, name, encryptedToken).Scan(
		&tenant.ID, &tenant.SlackTeamID, &tenant.Name, &tenant.BotToken,
		&tenant.BusinessType, &tenant.Timezone, &tenant.SetupComplete, &tenant.DefaultRoleID, &tenant.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("upserting tenant: %w", err)
	}
	return tenant, nil
}

func GetTenantBySlackTeamID(ctx context.Context, pool *pgxpool.Pool, slackTeamID string) (*Tenant, error) {
	tenant := &Tenant{}
	err := pool.QueryRow(ctx, `
		SELECT id, slack_team_id, name, bot_token, business_type, timezone, setup_complete, default_role_id, created_at
		FROM tenants WHERE slack_team_id = $1
	`, slackTeamID).Scan(
		&tenant.ID, &tenant.SlackTeamID, &tenant.Name, &tenant.BotToken,
		&tenant.BusinessType, &tenant.Timezone, &tenant.SetupComplete, &tenant.DefaultRoleID, &tenant.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // not found is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("getting tenant: %w", err)
	}
	return tenant, nil
}

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

func SetDefaultRole(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, roleID *uuid.UUID) error {
	_, err := pool.Exec(ctx, `
		UPDATE tenants SET default_role_id = $2 WHERE id = $1
	`, tenantID, roleID)
	if err != nil {
		return fmt.Errorf("setting default role: %w", err)
	}
	return nil
}
