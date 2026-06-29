package models

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DisabledApps returns the set of app names a tenant has explicitly disabled.
// The absence of a row means the app is enabled (default-on), so this returns
// only the disabled names — callers treat "not in this set" as enabled.
func DisabledApps(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) ([]string, error) {
	rows, err := pool.Query(ctx,
		`SELECT app_name FROM tenant_app_enablement
		 WHERE tenant_id = $1 AND enabled = false`,
		tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("loading disabled apps: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scanning disabled app: %w", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating disabled apps: %w", err)
	}
	return names, nil
}

// SetAppEnabled upserts a tenant's enablement choice for a single app. Because
// default is enabled, re-enabling stores an explicit enabled=true row rather
// than deleting — keeping an audit trail of when the toggle last changed.
func SetAppEnabled(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, appName string, enabled bool) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO tenant_app_enablement (tenant_id, app_name, enabled, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (tenant_id, app_name) DO UPDATE
		SET enabled = EXCLUDED.enabled, updated_at = now()`,
		tenantID, appName, enabled,
	)
	if err != nil {
		return fmt.Errorf("setting app enablement: %w", err)
	}
	return nil
}
