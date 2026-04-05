package services

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
)

// TenantTools defines the shared tool metadata for tenant operations.
var TenantTools = []ToolMeta{
	{Name: "update_tenant", Description: "Update the organization's business info and mark setup as complete.", Schema: propsReq(map[string]any{
		"business_type":  field("string", "Type of business (e.g., 'brewery', 'nonprofit')"),
		"timezone":       field("string", "IANA timezone (e.g., 'America/Denver')"),
		"setup_complete": map[string]any{"type": "boolean", "description": "Mark setup as complete"},
	}, "business_type"), AdminOnly: true},
}

// TenantService handles tenant operations with authorization.
type TenantService struct {
	pool *pgxpool.Pool
}

// Update updates tenant business info. Admin only.
func (s *TenantService) Update(ctx context.Context, c *Caller, businessType, timezone string) error {
	if !c.IsAdmin {
		return ErrForbidden
	}
	if timezone == "" {
		timezone = "UTC"
	}
	return models.UpdateTenantSetup(ctx, s.pool, c.TenantID, businessType, timezone)
}
