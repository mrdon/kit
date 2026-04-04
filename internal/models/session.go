package models

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Session struct {
	ID             uuid.UUID
	TenantID       uuid.UUID
	SlackThreadTS  string
	SlackChannelID string
	UserID         uuid.UUID
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// GetOrCreateSession finds or creates a session by tenant + channel + thread_ts.
func GetOrCreateSession(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, channelID, threadTS string, userID uuid.UUID) (*Session, error) {
	session := &Session{}
	err := pool.QueryRow(ctx, `
		INSERT INTO sessions (id, tenant_id, slack_channel_id, slack_thread_ts, user_id)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (tenant_id, slack_channel_id, slack_thread_ts)
		DO UPDATE SET updated_at = now()
		RETURNING id, tenant_id, slack_channel_id, slack_thread_ts, user_id, created_at, updated_at
	`, uuid.New(), tenantID, channelID, threadTS, userID).Scan(
		&session.ID, &session.TenantID, &session.SlackChannelID, &session.SlackThreadTS,
		&session.UserID, &session.CreatedAt, &session.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get or create session: %w", err)
	}
	return session, nil
}
