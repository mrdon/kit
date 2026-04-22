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

type Session struct {
	ID             uuid.UUID
	TenantID       uuid.UUID
	SlackThreadTS  string
	SlackChannelID string
	UserID         uuid.UUID
	BotInitiated   bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// CreateSession creates a new session with a unique thread_ts.
// botInitiated=true means Kit started the thread (scheduled task, onboarding DM);
// such sessions route any in-thread message back to the agent. Human-initiated
// sessions only route explicit @-mentions.
func CreateSession(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, channelID, threadTS string, userID uuid.UUID, botInitiated bool) (*Session, error) {
	session := &Session{}
	err := pool.QueryRow(ctx, `
		INSERT INTO sessions (id, tenant_id, slack_channel_id, slack_thread_ts, user_id, bot_initiated)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, tenant_id, slack_channel_id, slack_thread_ts, user_id, bot_initiated, created_at, updated_at
	`, uuid.New(), tenantID, channelID, threadTS, userID, botInitiated).Scan(
		&session.ID, &session.TenantID, &session.SlackChannelID, &session.SlackThreadTS,
		&session.UserID, &session.BotInitiated, &session.CreatedAt, &session.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("creating session: %w", err)
	}
	return session, nil
}

// GetSession fetches a single session by ID. Returns (nil, nil) if no such
// session exists, matching the other get-by-id helpers in this package.
func GetSession(ctx context.Context, pool *pgxpool.Pool, tenantID, sessionID uuid.UUID) (*Session, error) {
	session := &Session{}
	err := pool.QueryRow(ctx, `
		SELECT id, tenant_id, slack_channel_id, slack_thread_ts, user_id, bot_initiated, created_at, updated_at
		FROM sessions
		WHERE tenant_id = $1 AND id = $2
	`, tenantID, sessionID).Scan(
		&session.ID, &session.TenantID, &session.SlackChannelID, &session.SlackThreadTS,
		&session.UserID, &session.BotInitiated, &session.CreatedAt, &session.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // not found is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("getting session: %w", err)
	}
	return session, nil
}

// ListRecentSessionsForUser returns the given user's recent sessions,
// ordered by updated_at descending.
func ListRecentSessionsForUser(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID, limit int) ([]Session, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := pool.Query(ctx, `
		SELECT id, tenant_id, slack_channel_id, slack_thread_ts, user_id, bot_initiated, created_at, updated_at
		FROM sessions
		WHERE tenant_id = $1 AND user_id = $2
		ORDER BY updated_at DESC
		LIMIT $3
	`, tenantID, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.ID, &s.TenantID, &s.SlackChannelID, &s.SlackThreadTS, &s.UserID, &s.BotInitiated, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning session: %w", err)
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

// FindSessionByThread returns the session for (tenant, channel, thread_ts)
// or nil if no such session exists. Unlike GetOrCreateSession, it does not
// create a session on miss.
func FindSessionByThread(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, channelID, threadTS string) (*Session, error) {
	session := &Session{}
	err := pool.QueryRow(ctx, `
		SELECT id, tenant_id, slack_channel_id, slack_thread_ts, user_id, bot_initiated, created_at, updated_at
		FROM sessions
		WHERE tenant_id = $1 AND slack_channel_id = $2 AND slack_thread_ts = $3
	`, tenantID, channelID, threadTS).Scan(
		&session.ID, &session.TenantID, &session.SlackChannelID, &session.SlackThreadTS,
		&session.UserID, &session.BotInitiated, &session.CreatedAt, &session.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // not found is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("finding session by thread: %w", err)
	}
	return session, nil
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
// Sessions created by this path are always human-initiated (from an inbound
// Slack event); bot_initiated defaults to false. Bot-initiated sessions go
// through CreateSession with botInitiated=true instead.
func GetOrCreateSession(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, channelID, threadTS string, userID uuid.UUID) (*Session, error) {
	session := &Session{}
	err := pool.QueryRow(ctx, `
		INSERT INTO sessions (id, tenant_id, slack_channel_id, slack_thread_ts, user_id)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (tenant_id, slack_channel_id, slack_thread_ts)
		DO UPDATE SET updated_at = now()
		RETURNING id, tenant_id, slack_channel_id, slack_thread_ts, user_id, bot_initiated, created_at, updated_at
	`, uuid.New(), tenantID, channelID, threadTS, userID).Scan(
		&session.ID, &session.TenantID, &session.SlackChannelID, &session.SlackThreadTS,
		&session.UserID, &session.BotInitiated, &session.CreatedAt, &session.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get or create session: %w", err)
	}
	return session, nil
}
