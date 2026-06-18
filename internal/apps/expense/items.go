package expense

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const itemColumns = `id, tenant_id, report_id, attachment_id, vendor, spent_on, amount_cents, tax_cents, category, note, sort_order, created_at, updated_at`

func scanItem(row interface{ Scan(...any) error }) (*Item, error) {
	var it Item
	var vendor, category, note *string
	var spentOn *time.Time
	err := row.Scan(
		&it.ID, &it.TenantID, &it.ReportID, &it.AttachmentID,
		&vendor, &spentOn, &it.AmountCents, &it.TaxCents,
		&category, &note, &it.SortOrder, &it.CreatedAt, &it.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if vendor != nil {
		it.Vendor = *vendor
	}
	if category != nil {
		it.Category = *category
	}
	if note != nil {
		it.Note = *note
	}
	it.SpentOn = spentOn
	return &it, nil
}

func createItem(ctx context.Context, pool *pgxpool.Pool, it *Item) error {
	return pool.QueryRow(ctx, `
		INSERT INTO app_expense_items
			(tenant_id, report_id, attachment_id, vendor, spent_on, amount_cents, tax_cents, category, note, sort_order)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9,
			COALESCE((SELECT max(sort_order)+1 FROM app_expense_items WHERE tenant_id = $1 AND report_id = $2), 0))
		RETURNING id, sort_order, created_at, updated_at`,
		it.TenantID, it.ReportID, it.AttachmentID, nilIfEmpty(it.Vendor), it.SpentOn,
		it.AmountCents, it.TaxCents, nilIfEmpty(it.Category), nilIfEmpty(it.Note),
	).Scan(&it.ID, &it.SortOrder, &it.CreatedAt, &it.UpdatedAt)
}

func getItem(ctx context.Context, pool *pgxpool.Pool, tenantID, itemID uuid.UUID) (*Item, error) {
	row := pool.QueryRow(ctx,
		`SELECT `+itemColumns+` FROM app_expense_items WHERE tenant_id = $1 AND id = $2`,
		tenantID, itemID)
	return scanItem(row)
}

func listItems(ctx context.Context, pool *pgxpool.Pool, tenantID, reportID uuid.UUID) ([]Item, error) {
	rows, err := pool.Query(ctx,
		`SELECT `+itemColumns+` FROM app_expense_items
		 WHERE tenant_id = $1 AND report_id = $2 ORDER BY sort_order ASC, created_at ASC`,
		tenantID, reportID)
	if err != nil {
		return nil, fmt.Errorf("listing items: %w", err)
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning item: %w", err)
		}
		out = append(out, *it)
	}
	return out, rows.Err()
}

// itemUpdate carries the editable item fields; only set what changed.
type itemUpdate struct {
	Vendor       *string
	SpentOn      *time.Time
	AmountCents  *int64
	TaxCents     *int64
	Category     *string
	Note         *string
	AttachmentID *uuid.UUID
}

func updateItem(ctx context.Context, pool *pgxpool.Pool, tenantID, itemID uuid.UUID, u itemUpdate) error {
	var sets []string
	args := []any{tenantID, itemID}
	argN := 2
	add := func(col string, v any) {
		argN++
		sets = append(sets, fmt.Sprintf("%s = $%d", col, argN))
		args = append(args, v)
	}
	if u.Vendor != nil {
		add("vendor", nilIfEmpty(*u.Vendor))
	}
	if u.SpentOn != nil {
		add("spent_on", *u.SpentOn)
	}
	if u.AmountCents != nil {
		add("amount_cents", *u.AmountCents)
	}
	if u.TaxCents != nil {
		add("tax_cents", *u.TaxCents)
	}
	if u.Category != nil {
		add("category", nilIfEmpty(*u.Category))
	}
	if u.Note != nil {
		add("note", nilIfEmpty(*u.Note))
	}
	if u.AttachmentID != nil {
		add("attachment_id", *u.AttachmentID)
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "updated_at = now()")
	q := fmt.Sprintf(`UPDATE app_expense_items SET %s WHERE tenant_id = $1 AND id = $2`, strings.Join(sets, ", "))
	if _, err := pool.Exec(ctx, q, args...); err != nil {
		return fmt.Errorf("updating item: %w", err)
	}
	return nil
}

func deleteItem(ctx context.Context, pool *pgxpool.Pool, tenantID, itemID uuid.UUID) error {
	if _, err := pool.Exec(ctx,
		`DELETE FROM app_expense_items WHERE tenant_id = $1 AND id = $2`, tenantID, itemID); err != nil {
		return fmt.Errorf("deleting item: %w", err)
	}
	return nil
}

// recomputeTotal recalculates a report's total_cents from its items. Called
// after every item mutation so denormalised total stays consistent.
func recomputeTotal(ctx context.Context, pool *pgxpool.Pool, tenantID, reportID uuid.UUID) error {
	if _, err := pool.Exec(ctx, `
		UPDATE app_expense_reports r
		SET total_cents = COALESCE(
			(SELECT sum(amount_cents) FROM app_expense_items
			 WHERE tenant_id = $1 AND report_id = $2), 0),
		    updated_at = now()
		WHERE r.tenant_id = $1 AND r.id = $2`,
		tenantID, reportID); err != nil {
		return fmt.Errorf("recomputing total: %w", err)
	}
	return nil
}
