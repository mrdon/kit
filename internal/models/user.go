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
	Timezone    string
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
		RETURNING id, tenant_id, slack_user_id, display_name, is_admin, timezone, created_at
	`, uuid.New(), tenantID, slackUserID, nilIfEmpty(displayName), isAdmin).Scan(
		&user.ID, &user.TenantID, &user.SlackUserID, &user.DisplayName,
		&user.IsAdmin, &user.Timezone, &user.CreatedAt,
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
		SELECT id, tenant_id, slack_user_id, display_name, is_admin, timezone, created_at
		FROM users WHERE tenant_id = $1 AND slack_user_id = $2
	`, tenantID, slackUserID).Scan(
		&user.ID, &user.TenantID, &user.SlackUserID, &user.DisplayName,
		&user.IsAdmin, &user.Timezone, &user.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // not found is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("getting user: %w", err)
	}
	return user, nil
}

// GetUserByID finds a user by tenant + user ID.
func GetUserByID(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID) (*User, error) {
	user := &User{}
	err := pool.QueryRow(ctx, `
		SELECT id, tenant_id, slack_user_id, display_name, is_admin, timezone, created_at
		FROM users WHERE tenant_id = $1 AND id = $2
	`, tenantID, userID).Scan(
		&user.ID, &user.TenantID, &user.SlackUserID, &user.DisplayName,
		&user.IsAdmin, &user.Timezone, &user.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // not found is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("getting user by id: %w", err)
	}
	return user, nil
}

// ListUsersByTenant returns all users for a tenant.
func ListUsersByTenant(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) ([]User, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, tenant_id, slack_user_id, display_name, is_admin, timezone, created_at
		FROM users WHERE tenant_id = $1
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("listing users: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.TenantID, &u.SlackUserID, &u.DisplayName,
			&u.IsAdmin, &u.Timezone, &u.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning user: %w", err)
		}
		users = append(users, u)
	}
	return users, nil
}

// UpdateUserProfile updates a user's display name and timezone.
func UpdateUserProfile(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID, displayName, timezone string) error {
	_, err := pool.Exec(ctx, `
		UPDATE users SET display_name = $3, timezone = $4 WHERE tenant_id = $1 AND id = $2
	`, tenantID, userID, nilIfEmpty(displayName), timezone)
	if err != nil {
		return fmt.Errorf("updating user profile: %w", err)
	}
	return nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
