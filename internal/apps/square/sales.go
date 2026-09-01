package square

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Reporting member names, pinned here rather than spread across callers.
// Sales.local_date and Sales.local_hour are flagged "stability: preview" by
// Square's own annotation and the Reporting API is in open beta, so this
// block is the single place a contract move has to be repaired.
const (
	mSalesNet       = "Sales.net_sales"
	mSalesGross     = "Sales.top_line_product_sales"
	mSalesCollected = "Sales.total_collected_amount"
	mSalesOrders    = "Sales.order_count"
	mSalesTips      = "Sales.tips_amount"
	mSalesTax       = "Sales.sales_tax_amount"
	mSalesDiscounts = "Sales.discounts_amount"
	mSalesComps     = "Sales.comps_amount"
	mSalesReturns   = "Sales.itemized_returns"
	mSalesGiftCards = "Sales.gift_card_sales_amount"
	mSalesSvcCharge = "Sales.service_charges_only_sum"

	dSalesDate     = "Sales.local_date"
	dSalesHour     = "Sales.local_hour"
	dSalesLocation = "Sales.location_id"
	dSalesLocName  = "Sales.location_name"
	dSalesTimezone = "Sales.location_timezone"
	tdSales        = "Sales.local_reporting_timestamp"

	mMixNet      = "ProductMixReport.net_sales"
	mMixGross    = "ProductMixReport.gross_sales"
	mMixUnits    = "ProductMixReport.items_sold_quantity"
	dMixCategory = "ProductMixReport.category_name"
	dMixItem     = "ProductMixReport.item_name"
	dMixLocation = "ProductMixReport.location_id"
	dMixDate     = "ProductMixReport.local_date"
	tdMix        = "ProductMixReport.local_reporting_timestamp"
)

// DailySales is one business day at one location, in integer cents.
//
// NetCents is the revenue metric. Square computes net_sales as gross less
// discounts, comps and returns, and it ALREADY excludes tax and tips —
// which matters because tips run ~20% of collected at a taproom, so
// reporting CollectedCents as "sales" would overstate by a quarter.
type DailySales struct {
	LocationID      string
	LocationName    string
	Timezone        string
	Date            time.Time
	NetCents        int64
	GrossCents      int64
	CollectedCents  int64
	DiscountsCents  int64 // magnitude; the API reports this signed negative
	CompsCents      int64 // magnitude; likewise signed negative
	ReturnsCents    int64 // already positive in the API
	GiftCardCents   int64 // a liability, deliberately not revenue
	ServiceChgCents int64
	TipsCents       int64
	TaxCents        int64
	OrderCount      int
}

// HourlySales is one local clock hour of one business day.
type HourlySales struct {
	LocationID string
	Date       time.Time
	Hour       int
	NetCents   int64
	OrderCount int
}

// ItemSales is one item's sales on one business day.
type ItemSales struct {
	LocationID string
	Date       time.Time
	Category   string
	Item       string
	NetCents   int64
	GrossCents int64
	Units      float64
}

// dateRange renders [start, end] as the full-timestamp bounds the Reporting
// API is happiest with. Square's docs note plain dates are accepted but
// full timestamps are safest for exact boundaries.
func dateRange(start, end time.Time) []string {
	return []string{
		start.Format("2006-01-02") + "T00:00:00.000",
		end.Format("2006-01-02") + "T23:59:59.999",
	}
}

// FetchDailySales returns per-business-day totals for [start, end].
func (c *Client) FetchDailySales(ctx context.Context, start, end time.Time) ([]DailySales, error) {
	rows, err := c.LoadReport(ctx, reportingQuery{
		Measures: []string{mSalesNet, mSalesGross, mSalesCollected, mSalesOrders,
			mSalesTips, mSalesTax, mSalesDiscounts, mSalesComps, mSalesReturns,
			mSalesGiftCards, mSalesSvcCharge},
		Dimensions: []string{dSalesLocation, dSalesLocName, dSalesTimezone, dSalesDate},
		TimeDimensions: []reportingTimeDimension{
			{Dimension: tdSales, DateRange: dateRange(start, end)},
		},
	})
	if err != nil {
		return nil, err
	}
	out := make([]DailySales, 0, len(rows))
	for _, r := range rows {
		d, err := dailyFromRow(r)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

func dailyFromRow(r reportingRow) (DailySales, error) {
	date, err := rowDate(r, dSalesDate)
	if err != nil {
		return DailySales{}, err
	}
	d := DailySales{
		LocationID:   rowString(r, dSalesLocation),
		LocationName: rowString(r, dSalesLocName),
		Timezone:     rowString(r, dSalesTimezone),
		Date:         date,
		OrderCount:   rowInt(r, mSalesOrders),
	}
	for _, f := range []struct {
		member string
		dst    *int64
	}{
		{mSalesNet, &d.NetCents}, {mSalesGross, &d.GrossCents},
		{mSalesCollected, &d.CollectedCents}, {mSalesTips, &d.TipsCents},
		{mSalesTax, &d.TaxCents}, {mSalesDiscounts, &d.DiscountsCents},
		{mSalesComps, &d.CompsCents}, {mSalesReturns, &d.ReturnsCents},
		{mSalesGiftCards, &d.GiftCardCents}, {mSalesSvcCharge, &d.ServiceChgCents},
	} {
		v, err := toCents(r[f.member])
		if err != nil {
			return DailySales{}, fmt.Errorf("%s on %s: %w", f.member, d.Date.Format(time.DateOnly), err)
		}
		*f.dst = v
	}
	// Discounts and comps arrive signed negative ("this reduced sales").
	// Store magnitudes so no downstream consumer subtracts them twice.
	d.DiscountsCents = abs64(d.DiscountsCents)
	d.CompsCents = abs64(d.CompsCents)
	d.ReturnsCents = abs64(d.ReturnsCents)
	return d, nil
}

// reconcileTolerance is how far the identities below may drift before it is
// worth saying something, in cents. Rounding each of six members
// independently can legitimately move the sum a cent or two.
const reconcileTolerance = 5

// Reconcile checks the two identities Square's Sales view guarantees:
//
//	net       = gross - discounts - comps - returns
//	collected = net + tax + tips + giftCards + serviceCharges
//
// Both held EXACTLY on every day sampled from the live account, so a
// deviation means a member's semantics moved under us. That is worth
// knowing loudly: this arithmetic is the input to every baseline, and a
// silent 25% error (mistaking collected for net, say) would look entirely
// plausible on the card. It returns an error for the caller to log rather
// than failing the sync, since the numbers stay broadly usable either way.
func (d DailySales) Reconcile() error {
	wantNet := d.GrossCents - d.DiscountsCents - d.CompsCents - d.ReturnsCents
	if diff := abs64(wantNet - d.NetCents); diff > reconcileTolerance {
		return fmt.Errorf("net sales identity off by %d cents on %s: gross %d - discounts %d - comps %d - returns %d = %d, but net = %d",
			diff, d.Date.Format(time.DateOnly), d.GrossCents, d.DiscountsCents,
			d.CompsCents, d.ReturnsCents, wantNet, d.NetCents)
	}
	// Gift card sales and service charges are collected but are not
	// revenue, so they sit in collected without ever reaching net. Omitting
	// them here produced a false alarm on a real day that sold $20.33 of
	// gift cards -- which is exactly the residual this term accounts for.
	wantCollected := d.NetCents + d.TaxCents + d.TipsCents + d.GiftCardCents + d.ServiceChgCents
	if diff := abs64(wantCollected - d.CollectedCents); diff > reconcileTolerance {
		return fmt.Errorf("collected identity off by %d cents on %s: net %d + tax %d + tips %d + gift cards %d + service charges %d = %d, but collected = %d",
			diff, d.Date.Format(time.DateOnly), d.NetCents, d.TaxCents, d.TipsCents,
			d.GiftCardCents, d.ServiceChgCents, wantCollected, d.CollectedCents)
	}
	return nil
}

// FetchHourlySales returns per-hour totals for [start, end]. Hours that
// sold nothing are simply absent — the caller materialises them as zeros.
func (c *Client) FetchHourlySales(ctx context.Context, start, end time.Time) ([]HourlySales, error) {
	rows, err := c.LoadReport(ctx, reportingQuery{
		Measures:   []string{mSalesNet, mSalesOrders},
		Dimensions: []string{dSalesLocation, dSalesDate, dSalesHour},
		TimeDimensions: []reportingTimeDimension{
			{Dimension: tdSales, DateRange: dateRange(start, end)},
		},
	})
	if err != nil {
		return nil, err
	}
	out := make([]HourlySales, 0, len(rows))
	for _, r := range rows {
		date, err := rowDate(r, dSalesDate)
		if err != nil {
			return nil, err
		}
		net, err := toCents(r[mSalesNet])
		if err != nil {
			return nil, fmt.Errorf("hourly net sales on %s: %w", date.Format(time.DateOnly), err)
		}
		out = append(out, HourlySales{
			LocationID: rowString(r, dSalesLocation),
			Date:       date,
			Hour:       rowInt(r, dSalesHour),
			NetCents:   net,
			OrderCount: rowInt(r, mSalesOrders),
		})
	}
	return out, nil
}

// FetchItemSales returns per-item totals for [start, end], with Square's
// seller-facing category name. Getting categories from the Orders API
// instead would need CATALOG_READ plus a local catalog cache; here they
// come free with the same request.
func (c *Client) FetchItemSales(ctx context.Context, start, end time.Time) ([]ItemSales, error) {
	rows, err := c.LoadReport(ctx, reportingQuery{
		Measures:   []string{mMixNet, mMixGross, mMixUnits},
		Dimensions: []string{dMixLocation, dMixDate, dMixCategory, dMixItem},
		TimeDimensions: []reportingTimeDimension{
			{Dimension: tdMix, DateRange: dateRange(start, end)},
		},
	})
	if err != nil {
		return nil, err
	}
	out := make([]ItemSales, 0, len(rows))
	for _, r := range rows {
		date, err := rowDate(r, dMixDate)
		if err != nil {
			return nil, err
		}
		net, err := toCents(r[mMixNet])
		if err != nil {
			return nil, fmt.Errorf("item net sales on %s: %w", date.Format(time.DateOnly), err)
		}
		gross, err := toCents(r[mMixGross])
		if err != nil {
			return nil, fmt.Errorf("item gross sales on %s: %w", date.Format(time.DateOnly), err)
		}
		out = append(out, ItemSales{
			LocationID: rowString(r, dMixLocation),
			Date:       date,
			Category:   rowString(r, dMixCategory),
			Item:       rowString(r, dMixItem),
			NetCents:   net,
			GrossCents: gross,
			Units:      rowFloat(r, mMixUnits),
		})
	}
	return out, nil
}

// toCents converts a Reporting API money value to integer cents.
//
// The API returns money as a JSON number in MAJOR units, and the values
// carry binary-float artifacts (2725.0899999999997, 947.9499999999998), so
// this rounds rather than truncating — truncation would lose a cent on
// roughly half of all days. Strings are parsed textually instead of through
// a float, in case Square ever switches representation.
func toCents(v any) (int64, error) {
	switch n := v.(type) {
	case nil:
		return 0, nil
	case float64:
		return int64(math.Round(n * 100)), nil
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return 0, fmt.Errorf("parsing money %q: %w", n.String(), err)
		}
		return int64(math.Round(f * 100)), nil
	case string:
		return centsFromDecimalString(n)
	default:
		return 0, fmt.Errorf("unexpected money type %T", v)
	}
}

// centsFromDecimalString parses "1250.5" / "-12.34" without going through a
// float, padding or truncating the fraction to exactly two places.
func centsFromDecimalString(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimLeft(s, "+-")
	whole, frac, _ := strings.Cut(s, ".")
	if len(frac) > 2 {
		frac = frac[:2]
	}
	for len(frac) < 2 {
		frac += "0"
	}
	if whole == "" {
		whole = "0"
	}
	n, err := strconv.ParseInt(whole+frac, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parsing money %q: %w", s, err)
	}
	if neg {
		n = -n
	}
	return n, nil
}

// rowDate reads a "YYYY-MM-DD" dimension as a plain calendar date in UTC,
// matching the DATE columns it is stored in.
func rowDate(r reportingRow, member string) (time.Time, error) {
	s := rowString(r, member)
	if s == "" {
		return time.Time{}, fmt.Errorf("row is missing %s", member)
	}
	if len(s) > 10 {
		s = s[:10] // tolerate a full timestamp if a granularity ever creeps in
	}
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parsing %s %q: %w", member, s, err)
	}
	return t, nil
}

func rowString(r reportingRow, member string) string {
	switch v := r[member].(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

func rowFloat(r reportingRow, member string) float64 {
	switch v := r[member].(type) {
	case float64:
		return v
	case json.Number:
		f, _ := v.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(v, 64)
		return f
	default:
		return 0
	}
}

func rowInt(r reportingRow, member string) int { return int(math.Round(rowFloat(r, member))) }

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// ListDailySales / ListHourlySales / ListItemSales load an authenticated
// client for the tenant and fetch, mirroring ListPublishedShifts.
func (a *App) ListDailySales(ctx context.Context, tenantID uuid.UUID, start, end time.Time) ([]DailySales, error) {
	c, err := a.LoadClient(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return c.FetchDailySales(ctx, start, end)
}

func (a *App) ListHourlySales(ctx context.Context, tenantID uuid.UUID, start, end time.Time) ([]HourlySales, error) {
	c, err := a.LoadClient(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return c.FetchHourlySales(ctx, start, end)
}

func (a *App) ListItemSales(ctx context.Context, tenantID uuid.UUID, start, end time.Time) ([]ItemSales, error) {
	c, err := a.LoadClient(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return c.FetchItemSales(ctx, start, end)
}
