package expense

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Policy is the tenant-wide expense approval policy. A report submitted in a
// tenant routes to ApproverUserID if set, else to members of ApproverRole, else
// (unconfigured) to the owning role / admins.
type Policy struct {
	ApproverRole   string     `json:"approver_role,omitempty"`
	ApproverUserID *uuid.UUID `json:"approver_user_id,omitempty"`
}

// loadPolicy returns the tenant's policy, or a zero Policy when none is set.
func loadPolicy(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (Policy, error) {
	var p Policy
	var role *string
	err := pool.QueryRow(ctx,
		`SELECT approver_role, approver_user_id FROM app_expense_policy WHERE tenant_id = $1`,
		tenantID,
	).Scan(&role, &p.ApproverUserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Policy{}, nil
	}
	if err != nil {
		return Policy{}, fmt.Errorf("loading expense policy: %w", err)
	}
	if role != nil {
		p.ApproverRole = *role
	}
	return p, nil
}

// savePolicy upserts the tenant's policy.
func savePolicy(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, p Policy) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO app_expense_policy (tenant_id, approver_role, approver_user_id, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (tenant_id) DO UPDATE
		SET approver_role = EXCLUDED.approver_role,
		    approver_user_id = EXCLUDED.approver_user_id,
		    updated_at = now()`,
		tenantID, nilIfEmpty(p.ApproverRole), p.ApproverUserID,
	)
	if err != nil {
		return fmt.Errorf("saving expense policy: %w", err)
	}
	return nil
}
