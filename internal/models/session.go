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

// CreateSession creates a new session with a unique thread_ts.
func CreateSession(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, channelID, threadTS string, userID uuid.UUID) (*Session, error) {
	session := &Session{}
	err := pool.QueryRow(ctx, `
		INSERT INTO sessions (id, tenant_id, slack_channel_id, slack_thread_ts, user_id)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, tenant_id, slack_channel_id, slack_thread_ts, user_id, created_at, updated_at
	`, uuid.New(), tenantID, channelID, threadTS, userID).Scan(
		&session.ID, &session.TenantID, &session.SlackChannelID, &session.SlackThreadTS,
		&session.UserID, &session.CreatedAt, &session.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("creating session: %w", err)
	}
	return session, nil
}

// GetSession fetches a single session by ID.
func GetSession(ctx context.Context, pool *pgxpool.Pool, tenantID, sessionID uuid.UUID) (*Session, error) {
	session := &Session{}
	err := pool.QueryRow(ctx, `
		SELECT id, tenant_id, slack_channel_id, slack_thread_ts, user_id, created_at, updated_at
		FROM sessions
		WHERE tenant_id = $1 AND id = $2
	`, tenantID, sessionID).Scan(
		&session.ID, &session.TenantID, &session.SlackChannelID, &session.SlackThreadTS,
		&session.UserID, &session.CreatedAt, &session.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("getting session: %w", err)
	}
	return session, nil
}

// ListRecentSessions returns recent sessions ordered by updated_at descending.
func ListRecentSessions(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, limit int) ([]Session, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := pool.Query(ctx, `
		SELECT id, tenant_id, slack_channel_id, slack_thread_ts, user_id, created_at, updated_at
		FROM sessions
		WHERE tenant_id = $1
		ORDER BY updated_at DESC
		LIMIT $2
	`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.ID, &s.TenantID, &s.SlackChannelID, &s.SlackThreadTS, &s.UserID, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning session: %w", err)
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

// UpdateSessionThreadTS replaces a session's slack_thread_ts. Used after a
// bot-initiated session (e.g. scheduled task) posts its first Slack message
// and we now know the real thread root ts.
func UpdateSessionThreadTS(ctx context.Context, pool *pgxpool.Pool, tenantID, sessionID uuid.UUID, threadTS string) error {
	_, err := pool.Exec(ctx, `
		UPDATE sessions
		SET slack_thread_ts = $1, updated_at = now()
		WHERE tenant_id = $2 AND id = $3
	`, threadTS, tenantID, sessionID)
	if err != nil {
		return fmt.Errorf("updating session thread_ts: %w", err)
	}
	return nil
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
