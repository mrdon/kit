package googlecalendar

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"time"
)

// apiBase is the Calendar API v3 root. The host is unchanged despite the
// docs rebranding under developers.google.com/workspace/calendar.
const apiBase = "https://www.googleapis.com/calendar/v3"

// httpClient is the shared client for outbound Google calls.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// APIError carries a non-2xx Google response for errors.As branching.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("google calendar API error (status %d): %s", e.StatusCode, e.Body)
}

// IsNotFound reports a 404 (or 410 Gone) — a delete of an already-absent
// event, which callers treat as success.
func (e *APIError) IsNotFound() bool {
	return e.StatusCode == http.StatusNotFound || e.StatusCode == http.StatusGone
}

// IsConflict reports a 409 — an insert whose client-specified id already
// exists, the signal to fall back to patch.
func (e *APIError) IsConflict() bool { return e.StatusCode == http.StatusConflict }

// Client is a thin authenticated wrapper over the Calendar API for one
// tenant's service account. It re-mints the access token on a 401.
type Client struct {
	accessToken string
	tokenExpiry time.Time
	mint        func(ctx context.Context) (string, time.Time, error)
}

// EventDateTime is one endpoint of an event. Use DateTime (RFC 3339) +
// TimeZone (IANA) for timed events.
type EventDateTime struct {
	DateTime string `json:"dateTime,omitempty"`
	Date     string `json:"date,omitempty"`
	TimeZone string `json:"timeZone,omitempty"`
}

// ExtendedProperties carries our idempotency/audit stamp. Private props are
// only visible on this copy of the event and are queryable via
// privateExtendedProperty.
type ExtendedProperties struct {
	Private map[string]string `json:"private,omitempty"`
}

// Event is the subset of the Calendar Event resource we read/write.
type Event struct {
	ID                 string              `json:"id,omitempty"`
	Summary            string              `json:"summary,omitempty"`
	Description        string              `json:"description,omitempty"`
	Location           string              `json:"location,omitempty"`
	Start              *EventDateTime      `json:"start,omitempty"`
	End                *EventDateTime      `json:"end,omitempty"`
	ExtendedProperties *ExtendedProperties `json:"extendedProperties,omitempty"`
	Status             string              `json:"status,omitempty"`
	HTMLLink           string              `json:"htmlLink,omitempty"`
}

// ensureToken mints an access token if none is cached or it's near expiry.
func (c *Client) ensureToken(ctx context.Context) error {
	if c.accessToken != "" && time.Until(c.tokenExpiry) > time.Minute {
		return nil
	}
	tok, exp, err := c.mint(ctx)
	if err != nil {
		return err
	}
	c.accessToken, c.tokenExpiry = tok, exp
	return nil
}

// doJSON issues a request, decoding into out. On a 401 it re-mints the token
// once and retries. body may be nil.
func (c *Client) doJSON(ctx context.Context, method, path string, body, out any) error {
	if err := c.ensureToken(ctx); err != nil {
		return err
	}
	err := c.doOnce(ctx, method, path, body, out)
	var apiErr *APIError
	if asAPIError(err, &apiErr) && apiErr.StatusCode == http.StatusUnauthorized {
		c.accessToken = "" // force a re-mint
		if err := c.ensureToken(ctx); err != nil {
			return err
		}
		return c.doOnce(ctx, method, path, body, out)
	}
	return err
}

func (c *Client) doOnce(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshaling request body: %w", err)
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, apiBase+path, reader)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("calling google %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading google response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return &APIError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}
	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decoding google response: %w", err)
		}
	}
	return nil
}

// InsertEvent creates an event (event.ID may be a client-specified id).
func (c *Client) InsertEvent(ctx context.Context, calendarID string, event *Event) (*Event, error) {
	var out Event
	path := fmt.Sprintf("/calendars/%s/events", url.PathEscape(calendarID))
	if err := c.doJSON(ctx, http.MethodPost, path, event, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateEvent fully replaces an existing event by id (PUT). A full replace
// (vs a partial patch) is used so representation changes — e.g. converting a
// timed event to an all-day one — apply cleanly.
func (c *Client) UpdateEvent(ctx context.Context, calendarID, eventID string, event *Event) (*Event, error) {
	var out Event
	path := fmt.Sprintf("/calendars/%s/events/%s", url.PathEscape(calendarID), url.PathEscape(eventID))
	if err := c.doJSON(ctx, http.MethodPut, path, event, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteEvent removes an event by id. A 404/410 (already gone) is treated
// as success.
func (c *Client) DeleteEvent(ctx context.Context, calendarID, eventID string) error {
	path := fmt.Sprintf("/calendars/%s/events/%s", url.PathEscape(calendarID), url.PathEscape(eventID))
	err := c.doJSON(ctx, http.MethodDelete, path, nil, nil)
	var apiErr *APIError
	if asAPIError(err, &apiErr) && apiErr.IsNotFound() {
		return nil
	}
	return err
}

// UpsertEvent inserts the event, falling back to patch on a 409 (the
// client-specified id already exists). This is the idempotent write.
func (c *Client) UpsertEvent(ctx context.Context, calendarID string, event *Event) (*Event, error) {
	out, err := c.InsertEvent(ctx, calendarID, event)
	var apiErr *APIError
	if asAPIError(err, &apiErr) && apiErr.IsConflict() {
		return c.UpdateEvent(ctx, calendarID, event.ID, event)
	}
	return out, err
}

// ListEventsByPrivateProperties returns events carrying ALL of the given
// private extended properties (the API ANDs repeated privateExtendedProperty
// params), paging through nextPageToken. This is how a reconciliation sweep
// finds exactly the events it owns — pass OwnerProps(appName, tenantID).
//
// An empty props map is rejected rather than treated as "match everything":
// callers use this to decide what may be deleted, and an unfiltered list
// would hand them the whole calendar.
func (c *Client) ListEventsByPrivateProperties(ctx context.Context, calendarID string, props map[string]string) ([]Event, error) {
	if len(props) == 0 {
		return nil, errors.New("listing events by private properties: at least one property required")
	}
	// Sorted so the query string is stable regardless of map iteration order.
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var out []Event
	pageToken := ""
	for {
		q := url.Values{}
		for _, k := range keys {
			q.Add("privateExtendedProperty", k+"="+props[k])
		}
		q.Set("showDeleted", "false")
		q.Set("maxResults", "2500")
		if pageToken != "" {
			q.Set("pageToken", pageToken)
		}
		path := fmt.Sprintf("/calendars/%s/events?%s", url.PathEscape(calendarID), q.Encode())
		var resp struct {
			Items         []Event `json:"items"`
			NextPageToken string  `json:"nextPageToken"`
		}
		if err := c.doJSON(ctx, http.MethodGet, path, nil, &resp); err != nil {
			return nil, err
		}
		out = append(out, resp.Items...)
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}
	return out, nil
}

func asAPIError(err error, target **APIError) bool {
	for err != nil {
		if e, ok := err.(*APIError); ok { //nolint:errorlint // direct type is what we store
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
