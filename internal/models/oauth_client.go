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

// OAuthClient represents a dynamically registered OAuth client (RFC 7591).
type OAuthClient struct {
	ID           uuid.UUID
	ClientID     string
	ClientSecret string
	RedirectURIs []string
	ClientName   *string
	CreatedAt    time.Time
}

// CreateOAuthClient registers a new OAuth client.
func CreateOAuthClient(ctx context.Context, pool *pgxpool.Pool, clientID, clientSecret string, redirectURIs []string, clientName string) (*OAuthClient, error) {
	c := &OAuthClient{}
	err := pool.QueryRow(ctx, `
		INSERT INTO oauth_clients (client_id, client_secret, redirect_uris, client_name)
		VALUES ($1, $2, $3, $4)
		RETURNING id, client_id, client_secret, redirect_uris, client_name, created_at
	`, clientID, clientSecret, redirectURIs, nilIfEmpty(clientName)).Scan(
		&c.ID, &c.ClientID, &c.ClientSecret, &c.RedirectURIs, &c.ClientName, &c.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("creating oauth client: %w", err)
	}
	return c, nil
}

// GetOAuthClient looks up a client by client_id.
func GetOAuthClient(ctx context.Context, pool *pgxpool.Pool, clientID string) (*OAuthClient, error) {
	c := &OAuthClient{}
	err := pool.QueryRow(ctx, `
		SELECT id, client_id, client_secret, redirect_uris, client_name, created_at
		FROM oauth_clients WHERE client_id = $1
	`, clientID).Scan(&c.ID, &c.ClientID, &c.ClientSecret, &c.RedirectURIs, &c.ClientName, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // not found is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("getting oauth client: %w", err)
	}
	return c, nil
}

// OAuthCode represents a short-lived authorization code.
type OAuthCode struct {
	Code          string
	ClientID      string
	TenantID      uuid.UUID
	UserID        uuid.UUID
	RedirectURI   string
	CodeChallenge string
	ExpiresAt     time.Time
}

// CreateOAuthCode stores an authorization code.
func CreateOAuthCode(ctx context.Context, pool *pgxpool.Pool, code, clientID string, tenantID, userID uuid.UUID, redirectURI, codeChallenge string, expiresAt time.Time) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO oauth_codes (code, client_id, tenant_id, user_id, redirect_uri, code_challenge, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, code, clientID, tenantID, userID, redirectURI, codeChallenge, expiresAt)
	if err != nil {
		return fmt.Errorf("creating oauth code: %w", err)
	}
	return nil
}

// ConsumeOAuthCode looks up and deletes an authorization code. Returns nil if not found or expired.
func ConsumeOAuthCode(ctx context.Context, pool *pgxpool.Pool, code string) (*OAuthCode, error) {
	c := &OAuthCode{}
	err := pool.QueryRow(ctx, `
		DELETE FROM oauth_codes WHERE code = $1 AND expires_at > now()
		RETURNING code, client_id, tenant_id, user_id, redirect_uri, code_challenge, expires_at
	`, code).Scan(&c.Code, &c.ClientID, &c.TenantID, &c.UserID, &c.RedirectURI, &c.CodeChallenge, &c.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // not found is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("consuming oauth code: %w", err)
	}
	return c, nil
}
