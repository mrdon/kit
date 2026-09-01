package squaresales

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DayRollup is one business day of sales at one location. Money is integer
// cents throughout; NetCents is the revenue metric (gross less discounts,
// comps and returns, already excluding tax and tips).
type DayRollup struct {
	LocationID   string
	LocationName string
	Date         time.Time // business date, a plain calendar date at midnight
	Timezone     string
	Currency     string

	NetCents        int64
	GrossCents      int64
	CollectedCents  int64
	DiscountsCents  int64
	CompsCents      int64
	ReturnsCents    int64
	TipsCents       int64
	TaxCents        int64
	GiftCardCents   int64
	ServiceChgCents int64
	OrderCount      int

	CardPostedAt *time.Time
}

// AvgTicketCents is net sales per order, 0 when there were no orders. A
// method rather than a column so it can never disagree with its inputs.
func (d DayRollup) AvgTicketCents() int64 {
	if d.OrderCount == 0 {
		return 0
	}
	return d.NetCents / int64(d.OrderCount)
}

// Open reports whether the day actually traded, and so whether it belongs
// in a baseline. Closed days are excluded -- a taproom shut for a holiday
// would otherwise read as "100% below normal" and flag every single time.
//
// The test is on money rather than order count because of a real row in
// this data: 2026-06-28 recorded one order for $0.00 while the POS was
// being set up. Counting that as a trading day would drag its weekday's
// median down for eight weeks.
func (d DayRollup) Open() bool { return d.NetCents > 0 }

// dayCols is the column list every daily read shares, in scan order.
const dayCols = `location_id, location_name, business_date, timezone, currency,
	net_sales_cents, gross_sales_cents, collected_cents, discounts_cents,
	comps_cents, returns_cents, tips_cents, tax_cents,
	gift_card_cents, service_charge_cents, order_count, card_posted_at`

// scanDay reads one daily row in dayCols order.
func scanDay(row pgx.Row) (DayRollup, error) {
	var d DayRollup
	err := row.Scan(&d.LocationID, &d.LocationName, &d.Date, &d.Timezone, &d.Currency,
		&d.NetCents, &d.GrossCents, &d.CollectedCents, &d.DiscountsCents,
		&d.CompsCents, &d.ReturnsCents, &d.TipsCents, &d.TaxCents,
		&d.GiftCardCents, &d.ServiceChgCents, &d.OrderCount, &d.CardPostedAt)
	return d, err
}

// upsertDaily replaces one business day's totals. Square amends past days
// (a void, a comp, a late refund), so re-pulling a day we already have is
// the normal case, not an error path.
func upsertDaily(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, d DayRollup) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO app_squaresales_daily
			(tenant_id, location_id, location_name, business_date, timezone, currency,
			 net_sales_cents, gross_sales_cents, collected_cents, discounts_cents,
			 comps_cents, returns_cents, tips_cents, tax_cents,
			 gift_card_cents, service_charge_cents, order_count, observed_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17, now())
		ON CONFLICT (tenant_id, location_id, business_date) DO UPDATE SET
			location_name     = EXCLUDED.location_name,
			timezone          = EXCLUDED.timezone,
			currency          = EXCLUDED.currency,
			net_sales_cents   = EXCLUDED.net_sales_cents,
			gross_sales_cents = EXCLUDED.gross_sales_cents,
			collected_cents   = EXCLUDED.collected_cents,
			discounts_cents   = EXCLUDED.discounts_cents,
			comps_cents       = EXCLUDED.comps_cents,
			returns_cents     = EXCLUDED.returns_cents,
			tips_cents        = EXCLUDED.tips_cents,
			tax_cents         = EXCLUDED.tax_cents,
			gift_card_cents   = EXCLUDED.gift_card_cents,
			service_charge_cents = EXCLUDED.service_charge_cents,
			order_count       = EXCLUDED.order_count,
			observed_at       = now()`,
		tenantID, d.LocationID, d.LocationName, d.Date, d.Timezone, d.Currency,
		d.NetCents, d.GrossCents, d.CollectedCents, d.DiscountsCents,
		d.CompsCents, d.ReturnsCents, d.TipsCents, d.TaxCents,
		d.GiftCardCents, d.ServiceChgCents, d.OrderCount,
	)
	if err != nil {
		return fmt.Errorf("upserting daily sales rollup: %w", err)
	}
	return nil
}

// getDaily returns one business day, or (nil, nil) if it hasn't been synced.
func getDaily(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, date time.Time) (*DayRollup, error) {
	d, err := scanDay(pool.QueryRow(ctx, `
		SELECT `+dayCols+`
		FROM app_squaresales_daily
		WHERE tenant_id = $1 AND business_date = $2
		ORDER BY location_id LIMIT 1`,
		tenantID, date))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // not synced is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("loading daily sales rollup: %w", err)
	}
	return &d, nil
}

// listDailyDates returns rollups for an explicit set of business dates --
// the same-weekday baseline (D-7, D-14 ... D-56), which the caller computes.
// Dates with no row are simply absent from the result.
func listDailyDates(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, dates []time.Time) ([]DayRollup, error) {
	rows, err := pool.Query(ctx, `
		SELECT `+dayCols+`
		FROM app_squaresales_daily
		WHERE tenant_id = $1 AND business_date = ANY($2)
		ORDER BY business_date`,
		tenantID, dates)
	if err != nil {
		return nil, fmt.Errorf("listing daily sales rollups: %w", err)
	}
	defer rows.Close()
	return collectDays(rows)
}

// listDailyRange returns rollups in [from, to] ascending -- used for the
// trailing-window comparisons and for coverage checks.
func listDailyRange(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, from, to time.Time) ([]DayRollup, error) {
	rows, err := pool.Query(ctx, `
		SELECT `+dayCols+`
		FROM app_squaresales_daily
		WHERE tenant_id = $1 AND business_date >= $2 AND business_date <= $3
		ORDER BY business_date`,
		tenantID, from, to)
	if err != nil {
		return nil, fmt.Errorf("listing daily sales range: %w", err)
	}
	defer rows.Close()
	return collectDays(rows)
}

func collectDays(rows pgx.Rows) ([]DayRollup, error) {
	var out []DayRollup
	for rows.Next() {
		d, err := scanDay(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning daily sales rollup: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// earliestDaily returns the oldest synced business date, or zero when the
// tenant has no rows. Because the sync writes a row for every date in the
// window it pulled -- including zero-sales days -- this is exact coverage
// rather than "the oldest day that happened to sell something", which is
// what lets backfill state live in the data instead of a separate table.
func earliestDaily(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (time.Time, error) {
	var t *time.Time
	err := pool.QueryRow(ctx,
		`SELECT MIN(business_date) FROM app_squaresales_daily WHERE tenant_id = $1`,
		tenantID).Scan(&t)
	if err != nil {
		return time.Time{}, fmt.Errorf("loading sales coverage: %w", err)
	}
	if t == nil {
		return time.Time{}, nil
	}
	return *t, nil
}

// nextUnpostedDate returns the most recent business date on or before
// notAfter that has no card yet. Working backwards from the most recent
// means a gap (Kit down for a day) posts the freshest day rather than
// dredging up a stale one.
func nextUnpostedDate(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, notAfter time.Time) (time.Time, bool, error) {
	var d *time.Time
	err := pool.QueryRow(ctx, `
		SELECT business_date FROM app_squaresales_daily
		WHERE tenant_id = $1 AND business_date <= $2 AND card_posted_at IS NULL
		ORDER BY business_date DESC LIMIT 1`,
		tenantID, notAfter).Scan(&d)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("finding unposted sales date: %w", err)
	}
	if d == nil {
		return time.Time{}, false, nil
	}
	return *d, true, nil
}

// markCardPosted stamps a business date as handled. Called whether or not a
// card was actually created -- a day judged unpostable (no sales, no
// baseline) must not be retried every morning forever.
func markCardPosted(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, date time.Time) error {
	_, err := pool.Exec(ctx, `
		UPDATE app_squaresales_daily SET card_posted_at = now()
		WHERE tenant_id = $1 AND business_date = $2 AND card_posted_at IS NULL`,
		tenantID, date)
	if err != nil {
		return fmt.Errorf("marking sales card posted: %w", err)
	}
	return nil
}

// locationIdentity returns a zero-valued DayRollup carrying the tenant's
// most recently seen location, timezone and currency -- the template for a
// closed-day row. ok is false before the first successful sync, when we
// have no way to know the location and inventing one would be worse than
// leaving the gap.
func locationIdentity(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (DayRollup, bool, error) {
	var d DayRollup
	err := pool.QueryRow(ctx, `
		SELECT location_id, location_name, timezone, currency
		FROM app_squaresales_daily
		WHERE tenant_id = $1
		ORDER BY business_date DESC
		LIMIT 1`, tenantID,
	).Scan(&d.LocationID, &d.LocationName, &d.Timezone, &d.Currency)
	if errors.Is(err, pgx.ErrNoRows) {
		return DayRollup{}, false, nil
	}
	if err != nil {
		return DayRollup{}, false, fmt.Errorf("loading sales location identity: %w", err)
	}
	return d, true, nil
}
