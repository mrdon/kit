package models

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WidgetToken authenticates an embed-script request from a tenant's
// website. The plaintext token is shown to the admin once at creation;
// only the SHA-256 hash is stored. AllowedOrigins is an exact-match
// allowlist against the Origin header — a hard 403 gate before any
// rate-limit work runs.
type WidgetToken struct {
	ID             uuid.UUID
	TenantID       uuid.UUID
	AllowedOrigins []string
	CreatedBy      uuid.UUID
	CreatedAt      time.Time
	LastUsedAt     *time.Time
	RevokedAt      *time.Time
}

// HashWidgetToken hashes a plaintext token for storage and lookup.
func HashWidgetToken(plaintext string) []byte {
	h := sha256.Sum256([]byte(plaintext))
	return h[:]
}

// CreateWidgetToken stores the hash and returns the row. The caller is
// responsible for generating the plaintext and showing it to the admin.
func CreateWidgetToken(ctx context.Context, pool *pgxpool.Pool, tenantID, createdBy uuid.UUID, tokenHash []byte, allowedOrigins []string) (*WidgetToken, error) {
	wt := &WidgetToken{}
	err := pool.QueryRow(ctx, `
		INSERT INTO widget_tokens (id, tenant_id, token_hash, allowed_origins, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, tenant_id, allowed_origins, created_by, created_at, last_used_at, revoked_at
	`, uuid.New(), tenantID, tokenHash, allowedOrigins, createdBy).Scan(
		&wt.ID, &wt.TenantID, &wt.AllowedOrigins, &wt.CreatedBy,
		&wt.CreatedAt, &wt.LastUsedAt, &wt.RevokedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("creating widget token: %w", err)
	}
	return wt, nil
}

// FindWidgetTokenByHash looks up an active (non-revoked) token by its
// hash. Returns (nil, nil) when no active token matches.
func FindWidgetTokenByHash(ctx context.Context, pool *pgxpool.Pool, tokenHash []byte) (*WidgetToken, error) {
	wt := &WidgetToken{}
	err := pool.QueryRow(ctx, `
		SELECT id, tenant_id, allowed_origins, created_by, created_at, last_used_at, revoked_at
		FROM widget_tokens
		WHERE token_hash = $1 AND revoked_at IS NULL
	`, tokenHash).Scan(
		&wt.ID, &wt.TenantID, &wt.AllowedOrigins, &wt.CreatedBy,
		&wt.CreatedAt, &wt.LastUsedAt, &wt.RevokedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // not found is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("finding widget token: %w", err)
	}
	return wt, nil
}

// ListActiveWidgetTokens returns this tenant's non-revoked tokens, newest
// first. The plaintext is never returned — display code synthesises a
// placeholder from the row ID.
func ListActiveWidgetTokens(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) ([]WidgetToken, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, tenant_id, allowed_origins, created_by, created_at, last_used_at, revoked_at
		FROM widget_tokens
		WHERE tenant_id = $1 AND revoked_at IS NULL
		ORDER BY created_at DESC
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("listing widget tokens: %w", err)
	}
	defer rows.Close()

	var tokens []WidgetToken
	for rows.Next() {
		var t WidgetToken
		if err := rows.Scan(&t.ID, &t.TenantID, &t.AllowedOrigins, &t.CreatedBy,
			&t.CreatedAt, &t.LastUsedAt, &t.RevokedAt); err != nil {
			return nil, fmt.Errorf("scanning widget token: %w", err)
		}
		tokens = append(tokens, t)
	}
	return tokens, rows.Err()
}

// RevokeWidgetToken marks the token revoked. Returns nil if no row
// matched (idempotent revoke).
func RevokeWidgetToken(ctx context.Context, pool *pgxpool.Pool, tenantID, tokenID uuid.UUID) error {
	_, err := pool.Exec(ctx, `
		UPDATE widget_tokens
		SET revoked_at = now()
		WHERE tenant_id = $1 AND id = $2 AND revoked_at IS NULL
	`, tenantID, tokenID)
	if err != nil {
		return fmt.Errorf("revoking widget token: %w", err)
	}
	return nil
}

// TouchWidgetTokenUsed updates last_used_at; best-effort, errors are
// logged by the caller and the request still proceeds.
func TouchWidgetTokenUsed(ctx context.Context, pool *pgxpool.Pool, tokenID uuid.UUID) error {
	_, err := pool.Exec(ctx, `
		UPDATE widget_tokens SET last_used_at = now() WHERE id = $1
	`, tokenID)
	return err
}

// OriginAllowed checks exact-match against the token's allowed_origins.
// Empty list means "no origins allowed" — admins must list at least one.
func (t *WidgetToken) OriginAllowed(origin string) bool {
	return slices.Contains(t.AllowedOrigins, origin)
}
