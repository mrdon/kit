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
	// UserID is uuid.Nil for anonymous widget sessions (slack_channel_id =
	// 'web:widget'). For every other channel it's the accountable person:
	// the one who typed, or the owner of the job that ran.
	UserID       uuid.UUID
	BotInitiated bool
	// ActorKind and ActorLabel say what ran the session when it wasn't the
	// person themself: "agent" and the job's name for a scheduled job. Empty
	// for a session a person drove.
	ActorKind  string
	ActorLabel string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// WidgetChannelID is the synthetic slack_channel_id for sessions created
// by the website chat widget. The session's user_id is NULL for these
// rows; the database CHECK constraint enforces that pairing.
const WidgetChannelID = "web:widget"

const sessionColumns = `id, tenant_id, slack_channel_id, slack_thread_ts, user_id, bot_initiated, actor_kind, actor_label, created_at, updated_at`

// scanSession reads one row of sessionColumns. The nullable columns
// (user_id for widget rows, the actor pair for person-driven rows) land as
// their zero values.
func scanSession(row pgx.Row) (*Session, error) {
	s := &Session{}
	var uid uuid.NullUUID
	var kind, label *string
	err := row.Scan(&s.ID, &s.TenantID, &s.SlackChannelID, &s.SlackThreadTS, &uid, &s.BotInitiated, &kind, &label, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if uid.Valid {
		s.UserID = uid.UUID
	}
	if kind != nil {
		s.ActorKind = *kind
	}
	if label != nil {
		s.ActorLabel = *label
	}
	return s, nil
}

// CreateSession creates a new session with a unique thread_ts.
// botInitiated=true means Kit started the thread (scheduled task, onboarding DM);
// such sessions route any in-thread message back to the agent. Human-initiated
// sessions only route explicit @-mentions.
func CreateSession(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, channelID, threadTS string, userID uuid.UUID, botInitiated bool) (*Session, error) {
	return insertSession(ctx, pool, tenantID, channelID, threadTS, userID, botInitiated, nil, nil)
}

// CreateAgentSession creates the bot-initiated session a scheduled job runs
// in. ownerID is the job's owner, who stays the accountable human; the
// actor columns record that it was the job, by name, that ran.
func CreateAgentSession(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, channelID, threadTS string, ownerID uuid.UUID, label string) (*Session, error) {
	kind := ActorKindAgent
	return insertSession(ctx, pool, tenantID, channelID, threadTS, ownerID, true, &kind, &label)
}

func insertSession(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, channelID, threadTS string, userID uuid.UUID, botInitiated bool, actorKind, actorLabel *string) (*Session, error) {
	s, err := scanSession(pool.QueryRow(ctx, `
		INSERT INTO sessions (id, tenant_id, slack_channel_id, slack_thread_ts, user_id, bot_initiated, actor_kind, actor_label)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING `+sessionColumns,
		uuid.New(), tenantID, channelID, threadTS, userID, botInitiated, actorKind, actorLabel))
	if err != nil {
		return nil, fmt.Errorf("creating session: %w", err)
	}
	return s, nil
}

// GetSession fetches a single session by ID. Returns (nil, nil) if no such
// session exists, matching the other get-by-id helpers in this package.
func GetSession(ctx context.Context, pool *pgxpool.Pool, tenantID, sessionID uuid.UUID) (*Session, error) {
	s, err := scanSession(pool.QueryRow(ctx, `
		SELECT `+sessionColumns+` FROM sessions WHERE tenant_id = $1 AND id = $2
	`, tenantID, sessionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // not found is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("getting session: %w", err)
	}
	return s, nil
}

// ListRecentSessionsForUser returns the given user's recent sessions,
// ordered by updated_at descending.
func ListRecentSessionsForUser(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID, limit int) ([]Session, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := pool.Query(ctx, `
		SELECT `+sessionColumns+` FROM sessions
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
		s, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning session: %w", err)
		}
		sessions = append(sessions, *s)
	}
	return sessions, rows.Err()
}

// FindSessionByThread returns the session for (tenant, channel, thread_ts)
// or nil if no such session exists. Unlike GetOrCreateSession, it does not
// create a session on miss.
func FindSessionByThread(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, channelID, threadTS string) (*Session, error) {
	s, err := scanSession(pool.QueryRow(ctx, `
		SELECT `+sessionColumns+` FROM sessions
		WHERE tenant_id = $1 AND slack_channel_id = $2 AND slack_thread_ts = $3
	`, tenantID, channelID, threadTS))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // not found is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("finding session by thread: %w", err)
	}
	return s, nil
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
	s, err := scanSession(pool.QueryRow(ctx, `
		INSERT INTO sessions (id, tenant_id, slack_channel_id, slack_thread_ts, user_id)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (tenant_id, slack_channel_id, slack_thread_ts)
		DO UPDATE SET updated_at = now()
		RETURNING `+sessionColumns,
		uuid.New(), tenantID, channelID, threadTS, userID))
	if err != nil {
		return nil, fmt.Errorf("get or create session: %w", err)
	}
	return s, nil
}

// GetOrCreateWidgetSession finds or creates an anonymous widget session
// keyed by the browser's conversation_id. user_id is always NULL for
// widget rows; the database CHECK constraint enforces that the NULL is
// only valid when slack_channel_id = WidgetChannelID.
func GetOrCreateWidgetSession(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, conversationID string) (*Session, error) {
	s, err := scanSession(pool.QueryRow(ctx, `
		INSERT INTO sessions (id, tenant_id, slack_channel_id, slack_thread_ts, user_id)
		VALUES ($1, $2, $3, $4, NULL)
		ON CONFLICT (tenant_id, slack_channel_id, slack_thread_ts)
		DO UPDATE SET updated_at = now()
		RETURNING `+sessionColumns,
		uuid.New(), tenantID, WidgetChannelID, conversationID))
	if err != nil {
		return nil, fmt.Errorf("get or create widget session: %w", err)
	}
	return s, nil
}
