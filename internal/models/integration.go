package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PendingStatus is the lifecycle state of a pending integration setup.
type PendingStatus string

const (
	PendingStatusPending  PendingStatus = "pending"
	PendingStatusConsumed PendingStatus = "consumed"
	PendingStatusExpired  PendingStatus = "expired"
)

// ErrPendingNotPending is returned when CompletePendingIntegration is
// called on a row that's already consumed or past its expiry.
var ErrPendingNotPending = errors.New("pending integration not in 'pending' state")

// ErrIntegrationForbidden is returned when a non-admin caller tries to
// delete an integration they don't own.
var ErrIntegrationForbidden = errors.New("not allowed to modify this integration")

// PendingIntegration is an in-flight integration config awaiting a secret
// submission via the signed-URL web form.
type PendingIntegration struct {
	ID           uuid.UUID
	TenantID     uuid.UUID
	CreatedBy    uuid.UUID
	Provider     string
	AuthType     string
	TargetUserID *uuid.UUID
	Status       PendingStatus
	CreatedAt    time.Time
	ExpiresAt    time.Time
	CompletedAt  *time.Time
}

// Integration is a live configured integration. Token fields are
// deliberately absent: the LLM-facing read path never touches ciphertext,
// so accidental serialization can't leak secrets. Apps that need the
// plaintext secret call GetIntegrationTokens and decrypt themselves.
type Integration struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	UserID    *uuid.UUID // nil for tenant-scoped
	Provider  string
	AuthType  string
	Username  string
	Config    map[string]any
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CreatePendingIntegration inserts a new pending integration row.
// targetUserID is nil for tenant-scoped types, the caller's user id for
// user-scoped types.
func CreatePendingIntegration(
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID, createdByUserID uuid.UUID,
	provider, authType string,
	targetUserID *uuid.UUID,
	ttl time.Duration,
) (*PendingIntegration, error) {
	p := &PendingIntegration{
		TenantID:     tenantID,
		CreatedBy:    createdByUserID,
		Provider:     provider,
		AuthType:     authType,
		TargetUserID: targetUserID,
		Status:       PendingStatusPending,
	}
	err := pool.QueryRow(ctx, `
		INSERT INTO pending_integrations (
			tenant_id, created_by, provider, auth_type, target_user_id,
			status, expires_at
		) VALUES ($1, $2, $3, $4, $5, 'pending', now() + $6::interval)
		RETURNING id, created_at, expires_at`,
		tenantID, createdByUserID, provider, authType, targetUserID,
		fmt.Sprintf("%d seconds", int(ttl.Seconds())),
	).Scan(&p.ID, &p.CreatedAt, &p.ExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("creating pending integration: %w", err)
	}
	return p, nil
}

// GetPendingIntegration loads a pending integration, tenant-scoped.
// Returns (nil, nil) if missing or expired.
func GetPendingIntegration(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID) (*PendingIntegration, error) {
	p := &PendingIntegration{}
	err := pool.QueryRow(ctx, `
		SELECT id, tenant_id, created_by, provider, auth_type, target_user_id,
		       status, created_at, expires_at, completed_at
		FROM pending_integrations
		WHERE tenant_id = $1 AND id = $2 AND expires_at > now()`,
		tenantID, id,
	).Scan(
		&p.ID, &p.TenantID, &p.CreatedBy, &p.Provider, &p.AuthType,
		&p.TargetUserID, &p.Status, &p.CreatedAt, &p.ExpiresAt, &p.CompletedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // not found is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("loading pending integration: %w", err)
	}
	return p, nil
}

// CompletePendingIntegration consumes the pending row and upserts the
// live integration in one transaction. Encrypted token arguments are
// ciphertext-only — this function never touches plaintext.
func CompletePendingIntegration(
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID, pendingID uuid.UUID,
	username *string,
	primaryEnc, secondaryEnc *string,
	config map[string]any,
) (uuid.UUID, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	var (
		provider, authType string
		targetUserID       *uuid.UUID
		status             string
	)
	err = tx.QueryRow(ctx, `
		SELECT provider, auth_type, target_user_id, status
		FROM pending_integrations
		WHERE tenant_id = $1 AND id = $2 AND expires_at > now()
		FOR UPDATE`,
		tenantID, pendingID,
	).Scan(&provider, &authType, &targetUserID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrPendingNotPending
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("loading pending for completion: %w", err)
	}
	if status != string(PendingStatusPending) {
		return uuid.Nil, ErrPendingNotPending
	}

	configJSON, err := json.Marshal(config)
	if err != nil {
		return uuid.Nil, fmt.Errorf("marshaling config: %w", err)
	}

	// On conflict (i.e. update), nil pointers for username/tokens mean
	// "keep the stored value" — the web handler passes nil for a secret
	// field whose input wasn't rendered on the update form. Config is
	// always fully replaced; clearing a non-secret field (empty map
	// entry) is a supported operation.
	var integrationID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO integrations (
			tenant_id, user_id, provider, auth_type,
			username, primary_token, secondary_token, config
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (tenant_id, provider, auth_type, user_id)
		DO UPDATE SET
			username        = COALESCE(EXCLUDED.username, integrations.username),
			primary_token   = COALESCE(EXCLUDED.primary_token, integrations.primary_token),
			secondary_token = COALESCE(EXCLUDED.secondary_token, integrations.secondary_token),
			config          = EXCLUDED.config,
			updated_at      = now()
		RETURNING id`,
		tenantID, targetUserID, provider, authType,
		username, primaryEnc, secondaryEnc, configJSON,
	).Scan(&integrationID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("upserting integration: %w", err)
	}

	_, err = tx.Exec(ctx, `
		UPDATE pending_integrations
		SET status = 'consumed', completed_at = now()
		WHERE tenant_id = $1 AND id = $2`,
		tenantID, pendingID,
	)
	if err != nil {
		return uuid.Nil, fmt.Errorf("marking pending consumed: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("committing: %w", err)
	}
	return integrationID, nil
}

// GetIntegration loads the live integration row for the given composite key.
// userID = nil targets the tenant-scoped row. Returns (nil, nil) if missing.
// Never returns token fields — the struct doesn't carry them.
func GetIntegration(
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID uuid.UUID,
	provider, authType string,
	userID *uuid.UUID,
) (*Integration, error) {
	row := pool.QueryRow(ctx, `
		SELECT id, tenant_id, user_id, provider, auth_type,
		       username, config, created_at, updated_at
		FROM integrations
		WHERE tenant_id = $1
		  AND provider = $2
		  AND auth_type = $3
		  AND user_id IS NOT DISTINCT FROM $4`,
		tenantID, provider, authType, userID,
	)
	i, err := scanIntegration(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // not found is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("loading integration: %w", err)
	}
	return i, nil
}

// GetIntegrationByID loads an integration by its primary key, tenant-scoped.
// Returns (nil, nil) if missing.
func GetIntegrationByID(
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID, id uuid.UUID,
) (*Integration, error) {
	row := pool.QueryRow(ctx, `
		SELECT id, tenant_id, user_id, provider, auth_type,
		       username, config, created_at, updated_at
		FROM integrations
		WHERE tenant_id = $1 AND id = $2`,
		tenantID, id,
	)
	i, err := scanIntegration(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // not found is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("loading integration: %w", err)
	}
	return i, nil
}

// GetIntegrationTokens returns the raw encrypted ciphertext for an
// integration's secret fields. Used only by apps that need to decrypt and
// use the plaintext. MUST NEVER be exposed to LLM-facing surfaces.
// Returns ("", "", nil) when both tokens are unset.
func GetIntegrationTokens(
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID, integrationID uuid.UUID,
) (primaryEnc, secondaryEnc string, err error) {
	var p, s *string
	err = pool.QueryRow(ctx, `
		SELECT primary_token, secondary_token
		FROM integrations
		WHERE tenant_id = $1 AND id = $2`,
		tenantID, integrationID,
	).Scan(&p, &s)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", fmt.Errorf("integration %s not found", integrationID)
	}
	if err != nil {
		return "", "", fmt.Errorf("loading integration tokens: %w", err)
	}
	if p != nil {
		primaryEnc = *p
	}
	if s != nil {
		secondaryEnc = *s
	}
	return primaryEnc, secondaryEnc, nil
}

// ListIntegrations returns integrations visible to the caller. When
// includeAll is true (admin caller), every row in the tenant is returned.
// Otherwise: tenant-scoped rows (user_id IS NULL) plus the caller's own
// user-scoped rows.
func ListIntegrations(
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID uuid.UUID,
	forUserID *uuid.UUID,
	includeAll bool,
) ([]Integration, error) {
	var (
		rows pgx.Rows
		err  error
	)
	if includeAll {
		rows, err = pool.Query(ctx, `
			SELECT id, tenant_id, user_id, provider, auth_type,
			       username, config, created_at, updated_at
			FROM integrations
			WHERE tenant_id = $1
			ORDER BY provider, auth_type, created_at`,
			tenantID,
		)
	} else {
		rows, err = pool.Query(ctx, `
			SELECT id, tenant_id, user_id, provider, auth_type,
			       username, config, created_at, updated_at
			FROM integrations
			WHERE tenant_id = $1
			  AND (user_id IS NULL OR user_id = $2)
			ORDER BY provider, auth_type, created_at`,
			tenantID, forUserID,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("listing integrations: %w", err)
	}
	defer rows.Close()

	var out []Integration
	for rows.Next() {
		i, err := scanIntegration(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning integration: %w", err)
		}
		out = append(out, *i)
	}
	return out, rows.Err()
}

// DeleteIntegration removes an integration row. Non-admin callers can
// only delete rows where user_id matches their own id; admins can delete
// any row in the tenant. Returns ErrIntegrationForbidden when the caller
// isn't allowed to touch the target row.
func DeleteIntegration(
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID, id, callerUserID uuid.UUID,
	isAdmin bool,
) error {
	// Load the row first so we can distinguish "not found" from "forbidden"
	// without handing the caller oracle information.
	var ownerUserID *uuid.UUID
	err := pool.QueryRow(ctx, `
		SELECT user_id FROM integrations WHERE tenant_id = $1 AND id = $2`,
		tenantID, id,
	).Scan(&ownerUserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("integration not found: %w", pgx.ErrNoRows)
	}
	if err != nil {
		return fmt.Errorf("loading integration for delete: %w", err)
	}

	if !isAdmin {
		if ownerUserID == nil || *ownerUserID != callerUserID {
			return ErrIntegrationForbidden
		}
	}

	_, err = pool.Exec(ctx,
		`DELETE FROM integrations WHERE tenant_id = $1 AND id = $2`,
		tenantID, id,
	)
	if err != nil {
		return fmt.Errorf("deleting integration: %w", err)
	}
	return nil
}

func scanIntegration(row interface{ Scan(...any) error }) (*Integration, error) {
	i := &Integration{}
	var username *string
	var configJSON []byte
	err := row.Scan(
		&i.ID, &i.TenantID, &i.UserID, &i.Provider, &i.AuthType,
		&username, &configJSON, &i.CreatedAt, &i.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if username != nil {
		i.Username = *username
	}
	if len(configJSON) > 0 {
		if err := json.Unmarshal(configJSON, &i.Config); err != nil {
			return nil, fmt.Errorf("unmarshaling config: %w", err)
		}
	}
	if i.Config == nil {
		i.Config = map[string]any{}
	}
	return i, nil
}
