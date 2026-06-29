package voting

import (
	"context"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
)

// Usage reports the number of active votes in the tenant.
func (a *VotingApp) Usage(ctx context.Context, tenantID uuid.UUID) (string, error) {
	if a.pool == nil {
		return "", nil
	}
	var n int
	err := a.pool.QueryRow(ctx,
		`SELECT count(*) FROM app_votes WHERE tenant_id = $1 AND status = 'active'`,
		tenantID,
	).Scan(&n)
	if err != nil {
		return "", err
	}
	return apps.CountLabel(n, "active vote", "active votes"), nil
}
