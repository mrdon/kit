package squaresales

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/square"
)

const (
	// syncWindowDays is how far back each incremental run re-pulls. A
	// ticket can be voided, comped or re-opened after close, so yesterday
	// is not final at 00:01. Three days is one cheap request per grain.
	syncWindowDays = 3

	// resettleWindowDays catches the slow tail: card refunds and disputes
	// that land days after the sale and quietly move a past day's net,
	// which the 3-day window would never revisit.
	resettleWindowDays = 30

	// bucketRetentionDays bounds the hourly and item tables. Both only
	// ever serve recent same-weekday comparisons; the daily table is never
	// pruned because it is one row per day and feeds seasonal context.
	bucketRetentionDays = 180

	// maxHistoryDays bounds the one-time initial pull. Square's Reporting
	// API keeps 15 months; a year and a bit is all the seasonal comparison
	// can use, and the whole thing is a handful of requests.
	maxHistoryDays = 400

	// syncDeadline bounds one run so a slow Reporting query can never
	// wedge the function lane.
	syncDeadline = 90 * time.Second

	// maxPlausibleAvgTicketCents guards against a unit misread. A taproom
	// average ticket is tens of dollars; $1,000 means money arrived in a
	// different unit than we think, and writing it would silently poison
	// every baseline it later appears in.
	maxPlausibleAvgTicketCents = 100_000
)

// SyncSummary counts what one run wrote.
type SyncSummary struct {
	Days  int
	Hours int
	Items int
}

// RunSync pulls recent sales and replaces the rollups for those days.
//
// On a tenant with no rows at all it pulls the full available history in
// one go -- for a single-location taproom that is a few requests, so the
// chunked-backfill machinery a larger dataset would need is deliberately
// absent. The full pull is keyed on the table being EMPTY rather than on
// comparing coverage against a horizon: Square returns nothing for dates
// before the business existed, so a horizon comparison would never be
// satisfied and every hourly run would re-pull everything.
func (a *App) RunSync(ctx context.Context, tenantID uuid.UUID, triggeredBy string) (SyncSummary, error) {
	ctx, cancel := context.WithTimeout(ctx, syncDeadline)
	defer cancel()

	started := time.Now()
	earliest, err := earliestDaily(ctx, a.pool, tenantID)
	if err != nil {
		return SyncSummary{}, err
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)
	days := syncWindowDays
	firstRun := earliest.IsZero()
	if firstRun {
		days = maxHistoryDays
	}
	// end is a day ahead so a location in a timezone ahead of UTC still
	// has today's partial figures covered; replacing a day is idempotent.
	start, end := today.AddDate(0, 0, -days), today.AddDate(0, 0, 1)

	sum, err := a.syncRange(ctx, tenantID, start, end, !firstRun)
	if err != nil {
		if errors.Is(err, square.ErrMissingScope) {
			a.auditScopeMissing(ctx, tenantID, err)
		}
		a.auditSyncFailed(ctx, tenantID, triggeredBy, err, time.Since(started))
		return SyncSummary{}, err
	}
	// Scheduled no-ops are not audited: this runs hourly, and recording a
	// zero every time would bury the runs that did something.
	if sum.Days > 0 || triggeredBy != "schedule" {
		a.auditSyncCompleted(ctx, tenantID, triggeredBy, sum, time.Since(started))
	}
	return sum, nil
}

// RunResettle re-pulls a month so late refunds and disputes correct the
// baseline, then prunes the bucket tables.
func (a *App) RunResettle(ctx context.Context, tenantID uuid.UUID) (SyncSummary, error) {
	ctx, cancel := context.WithTimeout(ctx, syncDeadline)
	defer cancel()

	started := time.Now()
	today := time.Now().UTC().Truncate(24 * time.Hour)
	sum, err := a.syncRange(ctx, tenantID, today.AddDate(0, 0, -resettleWindowDays), today.AddDate(0, 0, 1), true)
	if err != nil {
		if errors.Is(err, square.ErrMissingScope) {
			a.auditScopeMissing(ctx, tenantID, err)
		}
		a.auditSyncFailed(ctx, tenantID, "resettle", err, time.Since(started))
		return SyncSummary{}, err
	}
	if err := pruneBuckets(ctx, a.pool, tenantID, today.AddDate(0, 0, -bucketRetentionDays)); err != nil {
		return sum, err
	}
	a.auditSyncCompleted(ctx, tenantID, "resettle", sum, time.Since(started))
	return sum, nil
}

// syncRange pulls [start, end] and replaces every business day it covers.
// fillClosed writes explicit zero rows for dates Square reported nothing
// for, so a closed day is recorded as closed rather than missing; it is off
// for the initial history pull, where "no row" legitimately means the
// business did not exist yet.
func (a *App) syncRange(ctx context.Context, tenantID uuid.UUID, start, end time.Time, fillClosed bool) (SyncSummary, error) {
	daily, err := square.Instance().ListDailySales(ctx, tenantID, start, end)
	if err != nil {
		return SyncSummary{}, fmt.Errorf("fetching daily sales: %w", err)
	}
	hourly, err := square.Instance().ListHourlySales(ctx, tenantID, start, end)
	if err != nil {
		return SyncSummary{}, fmt.Errorf("fetching hourly sales: %w", err)
	}
	items, err := square.Instance().ListItemSales(ctx, tenantID, start, end)
	if err != nil {
		return SyncSummary{}, fmt.Errorf("fetching item sales: %w", err)
	}

	hoursByDay := groupHours(hourly)
	itemsByDay := groupItems(items)

	var sum SyncSummary
	seen := make(map[string]bool, len(daily))
	for _, d := range daily {
		if !a.writeDay(ctx, tenantID, d, hoursByDay, itemsByDay, &sum) {
			continue
		}
		seen[d.Date.Format(time.DateOnly)] = true
	}
	if fillClosed {
		n, err := a.fillClosedDays(ctx, tenantID, start, end, seen)
		if err != nil {
			return sum, err
		}
		sum.Days += n
	}
	return sum, nil
}

// writeDay validates and stores one business day. It returns false when the
// day was rejected, which is logged rather than failing the whole run --
// one suspect day should not block the other twenty-nine.
func (a *App) writeDay(ctx context.Context, tenantID uuid.UUID, d square.DailySales,
	hoursByDay map[string][]square.HourlySales, itemsByDay map[string][]square.ItemSales, sum *SyncSummary,
) bool {
	key := d.Date.Format(time.DateOnly)
	if err := d.Reconcile(); err != nil {
		// Not fatal: the figures are still broadly usable, and refusing to
		// store anything would be worse than storing something slightly
		// off. But it must be loud -- this is the arithmetic every
		// baseline is built on.
		slog.Warn("squaresales: sales identity did not reconcile", "tenant_id", tenantID, "date", key, "error", err)
	}
	if d.OrderCount > 0 && d.NetCents/int64(d.OrderCount) > maxPlausibleAvgTicketCents {
		slog.Error("squaresales: implausible average ticket, skipping day",
			"tenant_id", tenantID, "date", key, "net_cents", d.NetCents, "orders", d.OrderCount)
		return false
	}

	if err := upsertDaily(ctx, a.pool, tenantID, dayFromSquare(d)); err != nil {
		slog.Error("squaresales: storing day failed", "tenant_id", tenantID, "date", key, "error", err)
		return false
	}
	sum.Days++

	hours := hoursByDay[key]
	if err := replaceHourlyDay(ctx, a.pool, tenantID, d.LocationID, d.Date, hoursFromSquare(hours)); err != nil {
		slog.Error("squaresales: storing hours failed", "tenant_id", tenantID, "date", key, "error", err)
	} else {
		sum.Hours += hoursPerDay
	}

	its := itemsFromSquare(itemsByDay[key])
	if err := replaceItemsDay(ctx, a.pool, tenantID, d.LocationID, d.Date, its); err != nil {
		slog.Error("squaresales: storing items failed", "tenant_id", tenantID, "date", key, "error", err)
	} else {
		sum.Items += len(its)
	}
	return true
}

// fillClosedDays writes zero rows for dates in the window that Square
// reported nothing for. A taproom shut for a holiday is a fact worth
// storing: it keeps "no row" meaning "not synced", and it gives the card
// task a row to mark as handled rather than retrying that date forever.
func (a *App) fillClosedDays(ctx context.Context, tenantID uuid.UUID, start, end time.Time, seen map[string]bool) (int, error) {
	loc, ok, err := locationIdentity(ctx, a.pool, tenantID)
	if err != nil || !ok {
		// Without a previously stored row we don't know the location or
		// its timezone, and inventing them would be worse than leaving the
		// gap. The next successful day fills this in.
		return 0, err
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	var n int
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if d.After(today) || seen[d.Format(time.DateOnly)] {
			continue
		}
		zero := loc
		zero.Date = d
		if err := upsertDaily(ctx, a.pool, tenantID, zero); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func groupHours(in []square.HourlySales) map[string][]square.HourlySales {
	out := make(map[string][]square.HourlySales)
	for _, h := range in {
		k := h.Date.Format(time.DateOnly)
		out[k] = append(out[k], h)
	}
	return out
}

func groupItems(in []square.ItemSales) map[string][]square.ItemSales {
	out := make(map[string][]square.ItemSales)
	for _, it := range in {
		k := it.Date.Format(time.DateOnly)
		out[k] = append(out[k], it)
	}
	return out
}

func dayFromSquare(d square.DailySales) DayRollup {
	return DayRollup{
		LocationID: d.LocationID, LocationName: d.LocationName,
		Date: d.Date, Timezone: d.Timezone, Currency: "USD",
		NetCents: d.NetCents, GrossCents: d.GrossCents, CollectedCents: d.CollectedCents,
		DiscountsCents: d.DiscountsCents, CompsCents: d.CompsCents, ReturnsCents: d.ReturnsCents,
		TipsCents: d.TipsCents, TaxCents: d.TaxCents,
		GiftCardCents: d.GiftCardCents, ServiceChgCents: d.ServiceChgCents,
		OrderCount: d.OrderCount,
	}
}

func hoursFromSquare(in []square.HourlySales) []HourRollup {
	out := make([]HourRollup, 0, len(in))
	for _, h := range in {
		out = append(out, HourRollup{
			LocationID: h.LocationID, Date: h.Date, Hour: h.Hour,
			NetCents: h.NetCents, OrderCount: h.OrderCount,
		})
	}
	return out
}

func itemsFromSquare(in []square.ItemSales) []ItemRollup {
	out := make([]ItemRollup, 0, len(in))
	for _, it := range in {
		out = append(out, ItemRollup{
			LocationID: it.LocationID, Date: it.Date,
			Category: it.Category, Item: it.Item,
			NetCents: it.NetCents, GrossCents: it.GrossCents, Units: it.Units,
		})
	}
	return out
}
