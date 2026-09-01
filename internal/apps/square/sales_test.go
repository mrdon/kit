package square

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The float artifacts below are verbatim from a live Reporting API
// response. They are the reason toCents rounds instead of truncating:
// int64(2725.0899999999997*100) truncates to 272508, losing a cent, and
// that error compounds across every stored day.
func TestToCents(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want int64
	}{
		{"live float artifact", 2725.0899999999997, 272509},
		{"live float artifact 2", 947.9499999999998, 94795},
		{"live negative discount", -28.619999999999997, -2862},
		{"whole dollars", 688.0, 68800},
		{"live net sales", 688.38, 68838},
		{"zero", 0.0, 0},
		{"nil is zero", nil, 0},
		{"json.Number", json.Number("1250.50"), 125050},
		{"decimal string", "1250.50", 125050},
		{"decimal string one place", "1250.5", 125050},
		{"decimal string no fraction", "1250", 125000},
		{"negative string", "-12.34", -1234},
		{"string extra precision truncates", "1.239", 123},
		{"empty string", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toCents(tt.in)
			if err != nil {
				t.Fatalf("toCents(%v) error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("toCents(%v) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
	if _, err := toCents(struct{}{}); err == nil {
		t.Fatal("expected error for unsupported money type")
	}
}

// dailyFromRow is fed a row copied from a live response for 2026-08-02.
func TestDailyFromRowLiveShape(t *testing.T) {
	row := reportingRow{
		"Sales.local_date":             "2026-08-02",
		"Sales.location_id":            "LGZ07VJVEQRD6",
		"Sales.location_name":          "Gravity Brewing",
		"Sales.location_timezone":      "America/Denver",
		"Sales.net_sales":              688.38,
		"Sales.top_line_product_sales": 721.0,
		"Sales.total_collected_amount": 947.9499999999998,
		"Sales.order_count":            32.0,
		"Sales.tips_amount":            194.84,
		"Sales.sales_tax_amount":       64.73,
		"Sales.discounts_amount":       -28.619999999999997,
		"Sales.comps_amount":           -4.0,
		"Sales.itemized_returns":       0.0,
	}
	d, err := dailyFromRow(row)
	if err != nil {
		t.Fatalf("dailyFromRow: %v", err)
	}
	if d.NetCents != 68838 {
		t.Errorf("NetCents = %d, want 68838", d.NetCents)
	}
	if d.CollectedCents != 94795 {
		t.Errorf("CollectedCents = %d, want 94795", d.CollectedCents)
	}
	// Signed-negative in the API, stored as a magnitude so nothing
	// downstream subtracts it twice.
	if d.DiscountsCents != 2862 {
		t.Errorf("DiscountsCents = %d, want 2862 (magnitude)", d.DiscountsCents)
	}
	if d.OrderCount != 32 {
		t.Errorf("OrderCount = %d, want 32", d.OrderCount)
	}
	if !d.Date.Equal(time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Date = %v, want 2026-08-02 UTC", d.Date)
	}
	if d.Timezone != "America/Denver" {
		t.Errorf("Timezone = %q", d.Timezone)
	}
	// Tips and tax are reported but must never be folded into net sales:
	// net + tax + tips is what the customer paid, not what was sold.
	if d.NetCents+d.TaxCents+d.TipsCents != 68838+6473+19484 {
		t.Error("net/tax/tips were conflated")
	}
}

// A slow Reporting query answers HTTP 200 with a Continue-wait sentinel.
// Treating that as success yields a silently empty report, so the retry is
// load-bearing rather than defensive.
func TestLoadReportRetriesContinueWait(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_, _ = w.Write([]byte(`{"error":"Continue wait"}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"Sales.local_date":"2026-08-02","Sales.net_sales":688.38}]}`))
	}))
	defer srv.Close()

	c := &Client{apiBase: srv.URL, accessToken: "tok"}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rows, err := c.LoadReport(ctx, reportingQuery{Measures: []string{mSalesNet}})
	if err != nil {
		t.Fatalf("LoadReport: %v", err)
	}
	if calls != 2 {
		t.Fatalf("server calls = %d, want 2 (one wait, one result)", calls)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
}

// A 403 means the token lacks REPORTING_READ. It must surface as
// ErrMissingScope — not as a generic API error, and not via the 401
// refresh-and-retry path, which would refresh successfully and fail again.
func TestLoadReportForbiddenIsMissingScope(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"errors":[{"code":"FORBIDDEN"}]}`))
	}))
	defer srv.Close()

	c := &Client{apiBase: srv.URL, accessToken: "tok"}
	_, err := c.LoadReport(context.Background(), reportingQuery{Measures: []string{mSalesNet}})
	if !errors.Is(err, ErrMissingScope) {
		t.Fatalf("error = %v, want ErrMissingScope", err)
	}
	if calls != 1 {
		t.Fatalf("server calls = %d, want 1 (no retry on 403)", calls)
	}
}

func TestRowDate(t *testing.T) {
	if _, err := rowDate(reportingRow{}, dSalesDate); err == nil {
		t.Fatal("expected error for missing date")
	}
	// Tolerate a full timestamp in case a granularity ever creeps in.
	got, err := rowDate(reportingRow{dSalesDate: "2026-08-30T00:00:00.000"}, dSalesDate)
	if err != nil {
		t.Fatalf("rowDate: %v", err)
	}
	if got.Format(time.DateOnly) != "2026-08-30" {
		t.Fatalf("got %v", got)
	}
}

// Seven consecutive days copied verbatim from the live account. Both
// identities held exactly on all of them, which is what licenses treating
// net_sales as the revenue metric without recomputing it.
func TestReconcileLiveDays(t *testing.T) {
	days := []reportingRow{
		{"Sales.local_date": "2026-08-02", "Sales.top_line_product_sales": 721.0, "Sales.discounts_amount": -28.619999999999997, "Sales.comps_amount": -4.0, "Sales.itemized_returns": 0.0, "Sales.net_sales": 688.38, "Sales.total_collected_amount": 947.95, "Sales.tips_amount": 194.84, "Sales.sales_tax_amount": 64.72999999999999, "Sales.order_count": 32.0},
		{"Sales.local_date": "2026-08-03", "Sales.top_line_product_sales": 485.5, "Sales.discounts_amount": -40.95, "Sales.comps_amount": 0.0, "Sales.itemized_returns": 27.0, "Sales.net_sales": 417.55, "Sales.total_collected_amount": 573.18, "Sales.tips_amount": 113.84, "Sales.sales_tax_amount": 41.790000000000006, "Sales.order_count": 21.0},
		{"Sales.local_date": "2026-08-04", "Sales.top_line_product_sales": 891.5, "Sales.discounts_amount": -43.489999999999995, "Sales.comps_amount": -82.0, "Sales.itemized_returns": 0.0, "Sales.net_sales": 766.01, "Sales.total_collected_amount": 1041.84, "Sales.tips_amount": 181.72000000000003, "Sales.sales_tax_amount": 73.78, "Sales.gift_card_sales_amount": 20.33, "Sales.order_count": 53.0},
		{"Sales.local_date": "2026-08-05", "Sales.top_line_product_sales": 851.5, "Sales.discounts_amount": -18.180000000000003, "Sales.comps_amount": 0.0, "Sales.itemized_returns": 0.0, "Sales.net_sales": 833.32, "Sales.total_collected_amount": 1089.08, "Sales.tips_amount": 178.23999999999998, "Sales.sales_tax_amount": 77.51999999999998, "Sales.order_count": 48.0},
		{"Sales.local_date": "2026-08-06", "Sales.top_line_product_sales": 1164.5, "Sales.discounts_amount": -51.35, "Sales.comps_amount": -47.5, "Sales.itemized_returns": 0.0, "Sales.net_sales": 1065.65, "Sales.total_collected_amount": 1418.1399999999996, "Sales.tips_amount": 252.07000000000008, "Sales.sales_tax_amount": 100.42, "Sales.order_count": 64.0},
		{"Sales.local_date": "2026-08-07", "Sales.top_line_product_sales": 1486.0, "Sales.discounts_amount": -63.28000000000001, "Sales.comps_amount": -66.5, "Sales.itemized_returns": 0.0, "Sales.net_sales": 1356.2199999999998, "Sales.total_collected_amount": 1754.62, "Sales.tips_amount": 271.45000000000005, "Sales.sales_tax_amount": 126.95, "Sales.order_count": 65.0},
		{"Sales.local_date": "2026-08-08", "Sales.top_line_product_sales": 2985.0, "Sales.discounts_amount": -123.91, "Sales.comps_amount": -129.0, "Sales.itemized_returns": 7.0, "Sales.net_sales": 2725.09, "Sales.total_collected_amount": 3556.980000000001, "Sales.tips_amount": 574.3499999999998, "Sales.sales_tax_amount": 257.53999999999985, "Sales.order_count": 148.0},
	}
	for _, row := range days {
		d, err := dailyFromRow(row)
		if err != nil {
			t.Fatalf("dailyFromRow: %v", err)
		}
		if err := d.Reconcile(); err != nil {
			t.Errorf("%s: %v", d.Date.Format(time.DateOnly), err)
		}
	}
}

// A member whose meaning shifts under us must be caught, not averaged into
// a baseline. Here collected is passed off as net — a plausible-looking
// mistake that would inflate every reported figure by about 25%.
func TestReconcileCatchesConflatedTotal(t *testing.T) {
	d, err := dailyFromRow(reportingRow{
		"Sales.local_date": "2026-08-08", "Sales.top_line_product_sales": 2985.0,
		"Sales.discounts_amount": -123.91, "Sales.comps_amount": -129.0,
		"Sales.itemized_returns":       7.0,
		"Sales.net_sales":              3556.98, // wrong: this is total_collected
		"Sales.total_collected_amount": 3556.980000000001,
		"Sales.tips_amount":            574.3499999999998,
		"Sales.sales_tax_amount":       257.53999999999985,
	})
	if err != nil {
		t.Fatalf("dailyFromRow: %v", err)
	}
	if err := d.Reconcile(); err == nil {
		t.Fatal("expected reconcile to reject collected-as-net")
	}
}
