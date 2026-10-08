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

// Token kinds: what a "your sessions" listing would call the row.
const (
	TokenKindSession = "session"
	TokenKindMCP     = "mcp"
	TokenKindDevice  = "device"
)

// APIToken is an issued credential row. Exactly one of UserID and ActorID
// is set: a person's session or MCP token, or a paired device's session.
// The plaintext is minted and hashed by internal/auth/opaquetoken; rows
// hold only the hash.
type APIToken struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	UserID    *uuid.UUID
	ActorID   *uuid.UUID
	Kind      string
	Label     string
	ExpiresAt time.Time
}

// NewAPIToken is what CreateAPIToken inserts.
type NewAPIToken struct {
	TenantID  uuid.UUID
	UserID    *uuid.UUID
	ActorID   *uuid.UUID
	Kind      string
	Label     string
	TokenHash string
	ExpiresAt time.Time
}

// CreateAPIToken stores a hashed token. The database refuses a row that
// names both a user and an actor, or neither.
func CreateAPIToken(ctx context.Context, pool *pgxpool.Pool, t NewAPIToken) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO api_tokens (tenant_id, user_id, actor_id, kind, label, token_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, t.TenantID, t.UserID, t.ActorID, t.Kind, t.Label, t.TokenHash, t.ExpiresAt)
	if err != nil {
		return fmt.Errorf("creating api token: %w", err)
	}
	return nil
}

// LookupAPIToken finds a live token by hash. Returns nil if not found,
// expired or revoked.
func LookupAPIToken(ctx context.Context, pool *pgxpool.Pool, tokenHash string) (*APIToken, error) {
	t := &APIToken{}
	err := pool.QueryRow(ctx, `
		SELECT id, tenant_id, user_id, actor_id, kind, label, expires_at FROM api_tokens
		WHERE token_hash = $1 AND expires_at > now() AND revoked_at IS NULL
	`, tokenHash).Scan(&t.ID, &t.TenantID, &t.UserID, &t.ActorID, &t.Kind, &t.Label, &t.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // not found is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("looking up api token: %w", err)
	}
	return t, nil
}

// DeleteAPIToken removes an api_tokens row by its hash. Used by logout
// so a revoked session can't be reused even if its cookie is replayed.
// Missing rows are not an error (idempotent).
func DeleteAPIToken(ctx context.Context, pool *pgxpool.Pool, tokenHash string) error {
	if _, err := pool.Exec(ctx, `DELETE FROM api_tokens WHERE token_hash = $1`, tokenHash); err != nil {
		return fmt.Errorf("deleting api token: %w", err)
	}
	return nil
}

// DeleteExpiredAPITokens sweeps one tenant's rows that have been expired for
// longer than grace. LookupAPIToken already ignores expired rows, so the
// sweep is housekeeping, not enforcement; the grace keeps a row around long
// enough that "why did my session end" is still answerable from the table.
func DeleteExpiredAPITokens(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, grace time.Duration) (int64, error) {
	tag, err := pool.Exec(ctx, `
		DELETE FROM api_tokens
		WHERE tenant_id = $1 AND expires_at < now() - $2::interval
	`, tenantID, grace)
	if err != nil {
		return 0, fmt.Errorf("deleting expired api tokens: %w", err)
	}
	return tag.RowsAffected(), nil
}

// TouchAPIToken bumps last_used_at, at most once per touchInterval.
func TouchAPIToken(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, tokenHash string) error {
	_, err := pool.Exec(ctx, `
		UPDATE api_tokens SET last_used_at = now()
		WHERE tenant_id = $1 AND token_hash = $2
		  AND (last_used_at IS NULL OR last_used_at < now() - $3::interval)
	`, tenantID, tokenHash, touchInterval)
	if err != nil {
		return fmt.Errorf("touching api token: %w", err)
	}
	return nil
}

// ExtendAPIToken pushes a token's expiry out. Used for the sliding renewal
// of device sessions, which must outlive any one visit without ever being
// reissued by hand.
func ExtendAPIToken(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, tokenHash string, expiresAt time.Time) error {
	_, err := pool.Exec(ctx, `
		UPDATE api_tokens SET expires_at = $3
		WHERE tenant_id = $1 AND token_hash = $2 AND revoked_at IS NULL
	`, tenantID, tokenHash, expiresAt)
	if err != nil {
		return fmt.Errorf("extending api token: %w", err)
	}
	return nil
}
