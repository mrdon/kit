package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrEmailIntakeNotFound is returned by GetEmailIntake when the user has no
// intake row yet (never opted in).
var ErrEmailIntakeNotFound = errors.New("email intake not found")

// EmailIntake is a per-user email-to-task intake config plus its scan
// watermark (table app_task_email_intake). Opt-in: a row exists only for a
// mailbox user who enabled intake in the Tasks console. The sweep in the task
// app reads enabled rows, evaluates each row's cron schedule against
// LastScannedAt for due-ness, claims the row (ClaimedAt) to avoid a
// concurrent instance double-running it, then advances LastScannedAt.
type EmailIntake struct {
	ID                uuid.UUID
	TenantID          uuid.UUID
	UserID            uuid.UUID
	Enabled           bool
	Schedule          string
	ExtraInstructions string
	LastScannedAt     *time.Time
	ClaimedAt         *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func scanEmailIntake(row pgx.Row) (*EmailIntake, error) {
	var e EmailIntake
	if err := row.Scan(&e.ID, &e.TenantID, &e.UserID, &e.Enabled, &e.Schedule,
		&e.ExtraInstructions, &e.LastScannedAt, &e.ClaimedAt, &e.CreatedAt, &e.UpdatedAt); err != nil {
		return nil, err
	}
	return &e, nil
}

const emailIntakeCols = `id, tenant_id, user_id, enabled, schedule, extra_instructions,
	last_scanned_at, claimed_at, created_at, updated_at`

// GetEmailIntake returns the intake row for (tenant, user), or nil when the
// user has never configured intake.
func GetEmailIntake(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID) (*EmailIntake, error) {
	row := pool.QueryRow(ctx, `
		SELECT `+emailIntakeCols+`
		FROM app_task_email_intake
		WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, userID)
	e, err := scanEmailIntake(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrEmailIntakeNotFound
		}
		return nil, fmt.Errorf("getting email intake: %w", err)
	}
	return e, nil
}

// ListEnabledEmailIntakes returns all enabled intake rows for a tenant. The
// caller evaluates cron due-ness in Go (NextCronRun) and claims each row it
// intends to run.
func ListEnabledEmailIntakes(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) ([]EmailIntake, error) {
	rows, err := pool.Query(ctx, `
		SELECT `+emailIntakeCols+`
		FROM app_task_email_intake
		WHERE tenant_id = $1 AND enabled = true
		ORDER BY created_at`,
		tenantID)
	if err != nil {
		return nil, fmt.Errorf("listing enabled email intakes: %w", err)
	}
	defer rows.Close()

	var result []EmailIntake
	for rows.Next() {
		e, err := scanEmailIntake(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning email intake: %w", err)
		}
		result = append(result, *e)
	}
	return result, rows.Err()
}

// ClaimEmailIntake atomically stakes a claim on an enabled row so a second
// instance (e.g. during a rolling deploy) can't run the same sweep. Returns
// true only if this caller won the claim. leaseCutoff is now minus the claim
// lease: a stale claim left by a crashed sweep (claimed_at < leaseCutoff) is
// reclaimable. Cleared on success (AdvanceEmailIntakeWatermark) or failure
// (ReleaseEmailIntakeClaim).
func ClaimEmailIntake(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID, leaseCutoff time.Time) (bool, error) {
	tag, err := pool.Exec(ctx, `
		UPDATE app_task_email_intake
		SET claimed_at = now(), updated_at = now()
		WHERE tenant_id = $1 AND id = $2 AND enabled = true
		  AND (claimed_at IS NULL OR claimed_at < $3)`,
		tenantID, id, leaseCutoff)
	if err != nil {
		return false, fmt.Errorf("claiming email intake: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// AdvanceEmailIntakeWatermark records a successful sweep: moves the watermark
// to the newest email presented and releases the claim so the row is due again
// on its next scheduled tick.
func AdvanceEmailIntakeWatermark(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID, scannedTo time.Time) error {
	_, err := pool.Exec(ctx, `
		UPDATE app_task_email_intake
		SET last_scanned_at = $3, claimed_at = NULL, updated_at = now()
		WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, userID, scannedTo)
	if err != nil {
		return fmt.Errorf("advancing email intake watermark: %w", err)
	}
	return nil
}

// ReleaseEmailIntakeClaim clears a claim without moving the watermark, used on
// the failure path so the row retries on its next due tick rather than being
// blocked until the lease expires.
func ReleaseEmailIntakeClaim(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID) error {
	_, err := pool.Exec(ctx, `
		UPDATE app_task_email_intake
		SET claimed_at = NULL, updated_at = now()
		WHERE tenant_id = $1 AND user_id = $2`,
		tenantID, userID)
	if err != nil {
		return fmt.Errorf("releasing email intake claim: %w", err)
	}
	return nil
}

// UpsertEmailIntake writes the console-editable settings for (tenant, user),
// preserving the watermark and claim. Creates the row on first save.
func UpsertEmailIntake(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID, enabled bool, schedule, extraInstructions string) (*EmailIntake, error) {
	row := pool.QueryRow(ctx, `
		INSERT INTO app_task_email_intake (tenant_id, user_id, enabled, schedule, extra_instructions)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (tenant_id, user_id) DO UPDATE
		SET enabled = EXCLUDED.enabled,
		    schedule = EXCLUDED.schedule,
		    extra_instructions = EXCLUDED.extra_instructions,
		    updated_at = now()
		RETURNING `+emailIntakeCols,
		tenantID, userID, enabled, schedule, extraInstructions)
	e, err := scanEmailIntake(row)
	if err != nil {
		return nil, fmt.Errorf("upserting email intake: %w", err)
	}
	return e, nil
}
