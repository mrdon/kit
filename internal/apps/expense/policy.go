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
	// Public-intake config (see migration 063). When IntakeEnabled, the
	// /{slug}/expenses/submit page accepts anonymous receipt submissions that
	// land in IntakeRole and route through the normal approval policy.
	IntakeEnabled  bool   `json:"intake_enabled"`
	IntakeRole     string `json:"intake_role,omitempty"`
	IntakeCurrency string `json:"intake_currency,omitempty"`
}

// loadPolicy returns the tenant's policy, or a zero Policy when none is set.
func loadPolicy(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (Policy, error) {
	var p Policy
	var role, intakeRole, intakeCurrency *string
	err := pool.QueryRow(ctx,
		`SELECT approver_role, approver_user_id, intake_enabled, intake_role, intake_currency
		 FROM app_expense_policy WHERE tenant_id = $1`,
		tenantID,
	).Scan(&role, &p.ApproverUserID, &p.IntakeEnabled, &intakeRole, &intakeCurrency)
	if errors.Is(err, pgx.ErrNoRows) {
		return Policy{}, nil
	}
	if err != nil {
		return Policy{}, fmt.Errorf("loading expense policy: %w", err)
	}
	if role != nil {
		p.ApproverRole = *role
	}
	if intakeRole != nil {
		p.IntakeRole = *intakeRole
	}
	if intakeCurrency != nil {
		p.IntakeCurrency = *intakeCurrency
	}
	return p, nil
}

// savePolicy upserts the tenant's policy. Writes the full row, so callers that
// change only one facet (approver vs. intake) must start from a loaded Policy
// and mutate it — otherwise the other facet is reset.
func savePolicy(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, p Policy) error {
	currency := p.IntakeCurrency
	if currency == "" {
		currency = "USD"
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO app_expense_policy (tenant_id, approver_role, approver_user_id, intake_enabled, intake_role, intake_currency, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, now())
		ON CONFLICT (tenant_id) DO UPDATE
		SET approver_role = EXCLUDED.approver_role,
		    approver_user_id = EXCLUDED.approver_user_id,
		    intake_enabled = EXCLUDED.intake_enabled,
		    intake_role = EXCLUDED.intake_role,
		    intake_currency = EXCLUDED.intake_currency,
		    updated_at = now()`,
		tenantID, nilIfEmpty(p.ApproverRole), p.ApproverUserID,
		p.IntakeEnabled, nilIfEmpty(p.IntakeRole), currency,
	)
	if err != nil {
		return fmt.Errorf("saving expense policy: %w", err)
	}
	return nil
}
