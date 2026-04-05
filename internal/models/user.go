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

type User struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	SlackUserID string
	DisplayName *string
	IsAdmin     bool
	CreatedAt   time.Time
}

// GetOrCreateUser finds a user by tenant + slack_user_id, creating if needed.
func GetOrCreateUser(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, slackUserID string, displayName string, isAdmin bool) (*User, error) {
	user := &User{}
	err := pool.QueryRow(ctx, `
		INSERT INTO users (id, tenant_id, slack_user_id, display_name, is_admin)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (tenant_id, slack_user_id)
		DO UPDATE SET display_name = COALESCE(NULLIF(EXCLUDED.display_name, ''), users.display_name)
		RETURNING id, tenant_id, slack_user_id, display_name, is_admin, created_at
	`, uuid.New(), tenantID, slackUserID, nilIfEmpty(displayName), isAdmin).Scan(
		&user.ID, &user.TenantID, &user.SlackUserID, &user.DisplayName,
		&user.IsAdmin, &user.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get or create user: %w", err)
	}
	return user, nil
}

// GetUserBySlackID finds a user by tenant + slack_user_id.
func GetUserBySlackID(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, slackUserID string) (*User, error) {
	user := &User{}
	err := pool.QueryRow(ctx, `
		SELECT id, tenant_id, slack_user_id, display_name, is_admin, created_at
		FROM users WHERE tenant_id = $1 AND slack_user_id = $2
	`, tenantID, slackUserID).Scan(
		&user.ID, &user.TenantID, &user.SlackUserID, &user.DisplayName,
		&user.IsAdmin, &user.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // not found is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("getting user: %w", err)
	}
	return user, nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
