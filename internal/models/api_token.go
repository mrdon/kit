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

// APIToken represents an issued API token. The plaintext is minted and
// hashed by internal/auth/opaquetoken; rows hold only the hash.
type APIToken struct {
	ID       uuid.UUID
	TenantID uuid.UUID
	UserID   uuid.UUID
}

// CreateAPIToken stores a hashed API token.
func CreateAPIToken(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID, tokenHash string, expiresAt time.Time) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO api_tokens (tenant_id, user_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4)
	`, tenantID, userID, tokenHash, expiresAt)
	if err != nil {
		return fmt.Errorf("creating api token: %w", err)
	}
	return nil
}

// LookupAPIToken finds tenant and user by token hash. Returns nil if not found or expired.
func LookupAPIToken(ctx context.Context, pool *pgxpool.Pool, tokenHash string) (*APIToken, error) {
	t := &APIToken{}
	err := pool.QueryRow(ctx, `
		SELECT id, tenant_id, user_id FROM api_tokens
		WHERE token_hash = $1 AND expires_at > now()
	`, tokenHash).Scan(&t.ID, &t.TenantID, &t.UserID)
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
