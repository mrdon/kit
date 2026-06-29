package expense

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
)

// Usage reports the tenant's expense report count, calling out how many are
// pending approval (the actionable ones).
func (a *ExpenseApp) Usage(ctx context.Context, tenantID uuid.UUID) (string, error) {
	if a.svc == nil {
		return "", nil
	}
	var total, pending int
	err := a.svc.pool.QueryRow(ctx,
		`SELECT count(*),
		        count(*) FILTER (WHERE status = 'submitted')
		 FROM app_expense_reports WHERE tenant_id = $1`,
		tenantID,
	).Scan(&total, &pending)
	if err != nil {
		return "", err
	}
	label := apps.CountLabel(total, "report", "reports")
	if pending > 0 {
		return fmt.Sprintf("%s · %d pending approval", label, pending), nil
	}
	return label, nil
}
