package github

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Installation is one tenant's Kit GitHub App install record.
// AccountLogin / AccountType are lazy-populated — they require a
// signed JWT call to /app/installations/<id> we defer until a
// downstream feature actually needs them.
type Installation struct {
	TenantID          uuid.UUID
	InstallationID    int64
	AccountLogin      string
	AccountType       string // "User" or "Organization"
	InstalledByUserID *uuid.UUID
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// GetInstallation returns the install record for a tenant, or nil if
// the tenant hasn't installed the Kit GitHub App yet.
func GetInstallation(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (*Installation, error) {
	const q = `
        SELECT tenant_id,
               installation_id,
               account_login,
               account_type,
               installed_by_user_id,
               created_at,
               updated_at
        FROM app_github_installations
        WHERE tenant_id = $1`
	var inst Installation
	var login, atype *string
	err := pool.QueryRow(ctx, q, tenantID).Scan(
		&inst.TenantID,
		&inst.InstallationID,
		&login,
		&atype,
		&inst.InstalledByUserID,
		&inst.CreatedAt,
		&inst.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil //nolint:nilnil // explicit "not found" sentinel
		}
		return nil, fmt.Errorf("querying app_github_installations: %w", err)
	}
	if login != nil {
		inst.AccountLogin = *login
	}
	if atype != nil {
		inst.AccountType = *atype
	}
	return &inst, nil
}

// SaveInstallation upserts the install record. Called from the OAuth
// callback after GitHub redirects back with installation_id +
// setup_action=install. account_login + account_type are passed
// through as empty strings in v1 (we don't call GitHub at install
// time); a later commit can populate them via `GET
// /app/installations/<id>` when a downstream feature first needs the
// owner name.
func SaveInstallation(
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID uuid.UUID,
	installationID int64,
	accountLogin, accountType string,
	installedByUserID *uuid.UUID,
) error {
	const q = `
        INSERT INTO app_github_installations (
            tenant_id,
            installation_id,
            account_login,
            account_type,
            installed_by_user_id,
            updated_at
        )
        VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''), $5, now())
        ON CONFLICT (tenant_id) DO UPDATE SET
            installation_id      = EXCLUDED.installation_id,
            account_login        = COALESCE(EXCLUDED.account_login, app_github_installations.account_login),
            account_type         = COALESCE(EXCLUDED.account_type, app_github_installations.account_type),
            installed_by_user_id = COALESCE(EXCLUDED.installed_by_user_id, app_github_installations.installed_by_user_id),
            updated_at           = now()
        WHERE app_github_installations.tenant_id = $1`
	_, err := pool.Exec(ctx, q, tenantID, installationID, accountLogin, accountType, installedByUserID)
	if err != nil {
		return fmt.Errorf("saving github installation: %w", err)
	}
	return nil
}

// DeleteInstallation drops the row. Called when the user clicks
// Disconnect; the GitHub-side install is left for the user to remove
// manually on GitHub (we can't revoke their installation, only
// forget about it).
func DeleteInstallation(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) error {
	const q = `DELETE FROM app_github_installations WHERE tenant_id = $1`
	if _, err := pool.Exec(ctx, q, tenantID); err != nil {
		return fmt.Errorf("deleting github installation: %w", err)
	}
	return nil
}
