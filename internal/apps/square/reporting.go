package square

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// reportingPath is Square's Cube-powered analytics endpoint. It is a
// different beast from the /v2 REST endpoints: the request body is a Cube
// query, a slow query answers 200 with an error sentinel rather than a
// status code, and paging is limit/offset instead of a cursor.
const reportingPath = "/reporting/v1/load"

// continueWait is the body Square returns -- with HTTP 200 -- while a query
// is still running. It must be retried with the identical request; treating
// it as success yields a silently empty report.
const continueWait = "Continue wait"

// Reporting retry budget. Square documents the cubes as refreshing about
// every 15 minutes and warns off high-frequency polling, so a slow query is
// worth waiting on rather than failing fast, but not forever -- the sync
// task carries its own deadline above this.
const (
	reportingMaxAttempts = 5
	reportingBackoffBase = 2 * time.Second
)

// reportingPageLimit is rows per request. Our widest query is one row per
// item per day; a year of history at ~35 items a day stays inside a couple
// of pages.
const reportingPageLimit = 5000

// reportingMaxRows bounds a single LoadReport call. A runaway-loop backstop
// set far above any real query, not a tuning knob.
const reportingMaxRows = 100000

// reportingTimeDimension bounds a query's date range. Granularity is
// deliberately omitted by our callers: with one, the row key becomes
// "<view>.local_reporting_timestamp.day" and carries a full timestamp,
// whereas the plain local_date dimension gives a stable bare date.
type reportingTimeDimension struct {
	Dimension   string   `json:"dimension"`
	DateRange   []string `json:"dateRange,omitempty"`
	Granularity string   `json:"granularity,omitempty"`
}

// reportingQuery is a Cube query.
//
// There is deliberately no Order field: ordering by a measure is rejected
// with a 400 ("Invalid request"), and ordering by a dimension buys nothing
// we can't do in Go over a few hundred rows. Callers sort their own results.
type reportingQuery struct {
	Measures       []string                 `json:"measures,omitempty"`
	Dimensions     []string                 `json:"dimensions,omitempty"`
	TimeDimensions []reportingTimeDimension `json:"timeDimensions,omitempty"`
	Limit          int                      `json:"limit,omitempty"`
	Offset         int                      `json:"offset,omitempty"`
}

// reportingRow is one result row: member name -> value. Values arrive as
// JSON numbers (money in major units) or strings; see toCents / rowString.
type reportingRow map[string]any

type reportingRequest struct {
	Query reportingQuery `json:"query"`
}

type reportingResponse struct {
	Data []reportingRow `json:"data"`
	// Error carries the Continue-wait sentinel on an HTTP 200. A real
	// failure comes back as a non-2xx and is surfaced by doJSON instead.
	Error           string `json:"error"`
	LastRefreshTime string `json:"lastRefreshTime"`
}

// LoadReport runs a Cube query and returns every row, paging through
// limit/offset. It retries the Continue-wait sentinel and translates a 403
// into ErrMissingScope so a token without REPORTING_READ reads as an
// actionable message rather than a bare HTTP error.
func (c *Client) LoadReport(ctx context.Context, q reportingQuery) ([]reportingRow, error) {
	var out []reportingRow
	q.Limit = reportingPageLimit
	for q.Offset = 0; ; q.Offset += reportingPageLimit {
		page, err := c.loadPage(ctx, q)
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if len(page) < reportingPageLimit {
			return out, nil
		}
		if len(out) >= reportingMaxRows {
			return nil, fmt.Errorf("square reporting query returned more than %d rows", reportingMaxRows)
		}
	}
}

// loadPage issues one request, waiting out Continue-wait responses.
func (c *Client) loadPage(ctx context.Context, q reportingQuery) ([]reportingRow, error) {
	for attempt := range reportingMaxAttempts {
		var resp reportingResponse
		err := c.doJSON(ctx, http.MethodPost, reportingPath, reportingRequest{Query: q}, &resp)
		if err != nil {
			var apiErr *APIError
			if asAPIError(err, &apiErr) && apiErr.IsForbidden() {
				return nil, fmt.Errorf("%w (REPORTING_READ): %s", ErrMissingScope, apiErr.Body)
			}
			return nil, fmt.Errorf("loading square report: %w", err)
		}
		if resp.Error != continueWait {
			if resp.Error != "" {
				return nil, fmt.Errorf("square reporting error: %s", resp.Error)
			}
			return resp.Data, nil
		}
		slog.Debug("square reporting continue-wait", "attempt", attempt+1)
		if err := sleepCtx(ctx, reportingBackoffBase<<attempt); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("square reporting query still running after %d attempts", reportingMaxAttempts)
}

// sleepCtx waits for d, returning early if the context is cancelled.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
