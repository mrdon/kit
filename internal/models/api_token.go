package models

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// APIToken represents an issued API token.
type APIToken struct {
	ID       uuid.UUID
	TenantID uuid.UUID
	UserID   uuid.UUID
}

// GenerateToken creates a random opaque token and returns it along with its SHA-256 hash.
func GenerateToken() (token, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generating random bytes: %w", err)
	}
	token = "kit_" + hex.EncodeToString(b)
	h := sha256.Sum256([]byte(token))
	hash = hex.EncodeToString(h[:])
	return token, hash, nil
}

// HashToken returns the SHA-256 hash of a token string.
func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
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
