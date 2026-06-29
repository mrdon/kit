package task

import (
	"context"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
)

// Usage reports the count of open (not done/cancelled) tasks for the tenant.
func (a *TaskApp) Usage(ctx context.Context, tenantID uuid.UUID) (string, error) {
	if a.svc == nil {
		return "", nil
	}
	var n int
	err := a.svc.pool.QueryRow(ctx,
		`SELECT count(*) FROM app_tasks
		 WHERE tenant_id = $1 AND status NOT IN ('done', 'cancelled')`,
		tenantID,
	).Scan(&n)
	if err != nil {
		return "", err
	}
	return apps.CountLabel(n, "open task", "open tasks"), nil
}
