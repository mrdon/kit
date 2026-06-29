package calendar

import (
	"context"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
)

// Usage reports how many calendars the tenant has configured.
func (a *CalendarApp) Usage(ctx context.Context, tenantID uuid.UUID) (string, error) {
	if a.svc == nil {
		return "", nil
	}
	var n int
	err := a.svc.pool.QueryRow(ctx,
		`SELECT count(*) FROM app_calendars WHERE tenant_id = $1`,
		tenantID,
	).Scan(&n)
	if err != nil {
		return "", err
	}
	return apps.CountLabel(n, "calendar", "calendars"), nil
}
