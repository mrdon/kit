package coordination

import (
	"context"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
)

// Usage reports the number of active coordinations in the tenant.
func (a *CoordinationApp) Usage(ctx context.Context, tenantID uuid.UUID) (string, error) {
	if a.pool == nil {
		return "", nil
	}
	active, err := ListActiveCoordinations(ctx, a.pool, tenantID)
	if err != nil {
		return "", err
	}
	return apps.CountLabel(len(active), "active coordination", "active coordinations"), nil
}
