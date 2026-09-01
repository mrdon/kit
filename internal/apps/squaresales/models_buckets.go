package squaresales

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// hoursPerDay is how many hourly rows every covered business day gets. All
// 24 are written even when most are zero -- see replaceHourlyDay.
const hoursPerDay = 24

// HourRollup is one hour of one business day. Hours the taproom was shut
// are stored as explicit zeros, so a missing row means "not synced".
type HourRollup struct {
	LocationID string
	Date       time.Time
	Hour       int
	NetCents   int64
	OrderCount int
}

// ItemRollup is one item's sales on one business day. Category is Square's
// seller-facing name ("Beer"), or "Uncategorized" when the item has none.
type ItemRollup struct {
	LocationID string
	Date       time.Time
	Category   string
	Item       string
	NetCents   int64
	GrossCents int64
	Units      float64
}

// replaceHourlyDay writes all 24 hours for one business day, filling the
// hours Square omitted with zeros.
//
// The Reporting API returns no row for an hour that sold nothing, so
// materialising the full day is what makes an absent row unambiguously mean
// "not synced" rather than "nobody came in". Dead-hour detection depends on
// that distinction: a genuinely empty 3pm is the signal, not missing data.
// It also fixes the row set, so the upsert alone is a complete replace and
// no stale-row hunt is needed.
func replaceHourlyDay(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, locationID string, date time.Time, hours []HourRollup) error {
	byHour := make(map[int]HourRollup, len(hours))
	for _, h := range hours {
		byHour[h.Hour] = h
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin hourly replace: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	for hour := range hoursPerDay {
		h := byHour[hour] // zero value when Square reported nothing
		_, err := tx.Exec(ctx, `
			INSERT INTO app_squaresales_hourly
				(tenant_id, location_id, business_date, hour_of_day,
				 net_sales_cents, order_count, observed_at)
			VALUES ($1,$2,$3,$4,$5,$6, now())
			ON CONFLICT (tenant_id, location_id, business_date, hour_of_day) DO UPDATE SET
				net_sales_cents = EXCLUDED.net_sales_cents,
				order_count     = EXCLUDED.order_count,
				observed_at     = now()`,
			tenantID, locationID, date, hour, h.NetCents, h.OrderCount)
		if err != nil {
			return fmt.Errorf("upserting hour %d of %s: %w", hour, date.Format(time.DateOnly), err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit hourly replace: %w", err)
	}
	return nil
}

// listHourlyDates returns hourly rows for an explicit set of business dates
// -- the target day plus its same-weekday baseline, in one query.
func listHourlyDates(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, dates []time.Time) ([]HourRollup, error) {
	rows, err := pool.Query(ctx, `
		SELECT location_id, business_date, hour_of_day, net_sales_cents, order_count
		FROM app_squaresales_hourly
		WHERE tenant_id = $1 AND business_date = ANY($2)
		ORDER BY business_date, hour_of_day`,
		tenantID, dates)
	if err != nil {
		return nil, fmt.Errorf("listing hourly sales: %w", err)
	}
	defer rows.Close()

	var out []HourRollup
	for rows.Next() {
		var h HourRollup
		if err := rows.Scan(&h.LocationID, &h.Date, &h.Hour, &h.NetCents, &h.OrderCount); err != nil {
			return nil, fmt.Errorf("scanning hourly sales: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// replaceItemsDay swaps in a business day's item rows.
//
// Delete-then-insert rather than upsert because the row set is VARIABLE: a
// day that had a "Food" line and, after a correction, no longer does would
// keep a stale row under a pure upsert. The hourly table can upsert because
// its 24 rows are fixed; this one cannot.
func replaceItemsDay(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, locationID string, date time.Time, items []ItemRollup) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin items replace: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	if _, err := tx.Exec(ctx, `
		DELETE FROM app_squaresales_items
		WHERE tenant_id = $1 AND location_id = $2 AND business_date = $3`,
		tenantID, locationID, date); err != nil {
		return fmt.Errorf("clearing item sales for %s: %w", date.Format(time.DateOnly), err)
	}

	for _, it := range items {
		if _, err := tx.Exec(ctx, `
			INSERT INTO app_squaresales_items
				(tenant_id, location_id, business_date, category_name, item_name,
				 net_sales_cents, gross_sales_cents, units_sold, observed_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8, now())
			ON CONFLICT (tenant_id, location_id, business_date, category_name, item_name)
			DO UPDATE SET
				net_sales_cents   = EXCLUDED.net_sales_cents,
				gross_sales_cents = EXCLUDED.gross_sales_cents,
				units_sold        = EXCLUDED.units_sold,
				observed_at       = now()`,
			tenantID, locationID, date, it.Category, it.Item,
			it.NetCents, it.GrossCents, it.Units); err != nil {
			return fmt.Errorf("inserting item sales %q: %w", it.Item, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit items replace: %w", err)
	}
	return nil
}

// listItemsDates returns item rows for an explicit set of business dates.
func listItemsDates(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, dates []time.Time) ([]ItemRollup, error) {
	rows, err := pool.Query(ctx, `
		SELECT location_id, business_date, category_name, item_name,
		       net_sales_cents, gross_sales_cents, units_sold
		FROM app_squaresales_items
		WHERE tenant_id = $1 AND business_date = ANY($2)
		ORDER BY business_date, net_sales_cents DESC`,
		tenantID, dates)
	if err != nil {
		return nil, fmt.Errorf("listing item sales: %w", err)
	}
	defer rows.Close()
	return collectItems(rows)
}

func collectItems(rows pgx.Rows) ([]ItemRollup, error) {
	var out []ItemRollup
	for rows.Next() {
		var it ItemRollup
		if err := rows.Scan(&it.LocationID, &it.Date, &it.Category, &it.Item,
			&it.NetCents, &it.GrossCents, &it.Units); err != nil {
			return nil, fmt.Errorf("scanning item sales: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// pruneBuckets drops hourly and item rows older than before. The daily
// table is never pruned -- it is small (one row per day) and is what the
// seasonal comparisons read.
func pruneBuckets(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, before time.Time) error {
	if _, err := pool.Exec(ctx,
		`DELETE FROM app_squaresales_hourly WHERE tenant_id = $1 AND business_date < $2`,
		tenantID, before); err != nil {
		return fmt.Errorf("pruning hourly sales: %w", err)
	}
	if _, err := pool.Exec(ctx,
		`DELETE FROM app_squaresales_items WHERE tenant_id = $1 AND business_date < $2`,
		tenantID, before); err != nil {
		return fmt.Errorf("pruning item sales: %w", err)
	}
	return nil
}
