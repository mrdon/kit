package builder

import (
	"context"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
)

// Usage reports how many builder apps the tenant has created.
func (a *App) Usage(ctx context.Context, tenantID uuid.UUID) (string, error) {
	if a.pool == nil {
		return "", nil
	}
	var n int
	err := a.pool.QueryRow(ctx,
		`SELECT count(*) FROM builder_apps WHERE tenant_id = $1`,
		tenantID,
	).Scan(&n)
	if err != nil {
		return "", err
	}
	return apps.CountLabel(n, "app", "apps"), nil
}
