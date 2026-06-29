package vault

import (
	"context"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
)

// Usage reports how many secrets the tenant has stored, for the admin Apps page.
func (a *App) Usage(ctx context.Context, tenantID uuid.UUID) (string, error) {
	if a.pool == nil {
		return "", nil
	}
	var n int
	err := a.pool.QueryRow(ctx,
		`SELECT count(*) FROM app_vault_entries WHERE tenant_id = $1`,
		tenantID,
	).Scan(&n)
	if err != nil {
		return "", err
	}
	return apps.CountLabel(n, "secret", "secrets"), nil
}
