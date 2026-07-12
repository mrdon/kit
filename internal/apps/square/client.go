package square

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// squareVersion pins the API version sent on every request. Scheduled
// shifts require ≥ 2025-05-21; pin the current dated version explicitly so
// the app's dashboard-default version drifting never changes behaviour.
const squareVersion = "2026-05-20"

// prodAPIBase / sandboxAPIBase are the REST roots. The OAuth token
// endpoint shares the same host.
const (
	prodAPIBase    = "https://connect.squareup.com"
	sandboxAPIBase = "https://connect.squareupsandbox.com"
)

// httpClient is the shared client for outbound Square calls. Labor search
// endpoints are quick; a generous-but-bounded timeout covers a slow page.
var httpClient = &http.Client{Timeout: 30 * time.Second}

// APIError carries a non-2xx Square response for errors.As branching.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("square API error (status %d): %s", e.StatusCode, e.Body)
}

// IsUnauthorized reports whether the error is a 401 — the signal to
// refresh the access token and retry once.
func (e *APIError) IsUnauthorized() bool { return e.StatusCode == http.StatusUnauthorized }

// Client is a thin authenticated wrapper over the Square REST API for one
// tenant. It refreshes its access token on a 401 and persists the new
// tokens through refreshAndPersist (wired by LoadClient). Not safe for
// concurrent use across goroutines that each mutate the token.
type Client struct {
	apiBase     string
	accessToken string

	// refreshAndPersist obtains a new access token (using the stored
	// refresh token + app credentials), persists it, and returns it. Nil
	// disables refresh (the call just surfaces the 401).
	refreshAndPersist func(ctx context.Context) (string, error)
}

// doJSON issues a JSON request and decodes the response into out. On a 401
// it refreshes the token once and retries. body may be nil for GET.
func (c *Client) doJSON(ctx context.Context, method, path string, body, out any) error {
	if err := c.doOnce(ctx, method, path, body, out); err != nil {
		var apiErr *APIError
		if c.refreshAndPersist != nil && asAPIError(err, &apiErr) && apiErr.IsUnauthorized() {
			newTok, rerr := c.refreshAndPersist(ctx)
			if rerr != nil {
				return fmt.Errorf("refreshing square token after 401: %w", rerr)
			}
			c.accessToken = newTok
			return c.doOnce(ctx, method, path, body, out)
		}
		return err
	}
	return nil
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
	req, err := http.NewRequestWithContext(ctx, method, c.apiBase+path, reader)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.accessToken)
	req.Header.Set("Square-Version", squareVersion)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("calling square %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading square response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return &APIError{StatusCode: resp.StatusCode, Body: string(respBody)}
	}
	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decoding square response: %w", err)
		}
	}
	return nil
}

func asAPIError(err error, target **APIError) bool {
	for err != nil {
		if e, ok := err.(*APIError); ok { //nolint:errorlint // direct type is what we store
			*target = e
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// Location is the subset of the Square Location object we use.
type Location struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Timezone string `json:"timezone"`
	Status   string `json:"status"`
}

// ListLocations returns every location for the merchant. The endpoint is
// not paginated. Requires MERCHANT_PROFILE_READ.
func (c *Client) ListLocations(ctx context.Context) ([]Location, error) {
	var resp struct {
		Locations []Location `json:"locations"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/v2/locations", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Locations, nil
}

// TeamMember is the subset of the Square TeamMember object we use.
type TeamMember struct {
	ID         string `json:"id"`
	GivenName  string `json:"given_name"`
	FamilyName string `json:"family_name"`
	Status     string `json:"status"`
}

// DisplayName joins the given and family name, falling back to the id when
// both are empty (Square allows nameless members).
func (t TeamMember) DisplayName() string {
	switch {
	case t.GivenName != "" && t.FamilyName != "":
		return t.GivenName + " " + t.FamilyName
	case t.GivenName != "":
		return t.GivenName
	case t.FamilyName != "":
		return t.FamilyName
	default:
		return t.ID
	}
}

// SearchTeamMembers pulls all team members (active + inactive) and returns
// a map keyed by member id for decorating shifts. Requires EMPLOYEES_READ.
func (c *Client) SearchTeamMembers(ctx context.Context) (map[string]TeamMember, error) {
	out := make(map[string]TeamMember)
	cursor := ""
	for {
		reqBody := map[string]any{"limit": 200}
		if cursor != "" {
			reqBody["cursor"] = cursor
		}
		var resp struct {
			TeamMembers []TeamMember `json:"team_members"`
			Cursor      string       `json:"cursor"`
		}
		if err := c.doJSON(ctx, http.MethodPost, "/v2/team-members/search", reqBody, &resp); err != nil {
			return nil, err
		}
		for _, m := range resp.TeamMembers {
			out[m.ID] = m
		}
		if resp.Cursor == "" {
			break
		}
		cursor = resp.Cursor
	}
	return out, nil
}

// ScheduledShiftDetails is one copy (draft or published) of a scheduled
// shift's assignment. Times are RFC 3339 in the location's local time zone.
type ScheduledShiftDetails struct {
	TeamMemberID string `json:"team_member_id"`
	LocationID   string `json:"location_id"`
	JobID        string `json:"job_id"`
	StartAt      string `json:"start_at"`
	EndAt        string `json:"end_at"`
	Notes        string `json:"notes"`
	Timezone     string `json:"timezone"`
	IsDeleted    bool   `json:"is_deleted"`
}

// ScheduledShift is a planned shift with draft and (once published)
// published detail copies. A shift is "published" iff PublishedShiftDetails
// is non-nil.
type ScheduledShift struct {
	ID                    string                 `json:"id"`
	Version               int                    `json:"version"`
	DraftShiftDetails     *ScheduledShiftDetails `json:"draft_shift_details"`
	PublishedShiftDetails *ScheduledShiftDetails `json:"published_shift_details"`
	CreatedAt             string                 `json:"created_at"`
	UpdatedAt             string                 `json:"updated_at"`
}

// SearchPublishedShifts returns PUBLISHED scheduled shifts whose start
// falls in [startAt, endAt) for the given locations, paging through the
// cursor. locationIDs empty means all locations. Requires TIMECARDS_READ.
func (c *Client) SearchPublishedShifts(ctx context.Context, locationIDs []string, startAt, endAt time.Time) ([]ScheduledShift, error) {
	filter := map[string]any{
		"scheduled_shift_statuses": []string{"PUBLISHED"},
		"start": map[string]string{
			"start_at": startAt.UTC().Format(time.RFC3339),
			"end_at":   endAt.UTC().Format(time.RFC3339),
		},
	}
	if len(locationIDs) > 0 {
		filter["location_ids"] = locationIDs
	}

	var out []ScheduledShift
	cursor := ""
	for {
		query := map[string]any{
			"filter": filter,
			"sort":   map[string]string{"field": "START_AT", "order": "ASC"},
		}
		reqBody := map[string]any{"query": query, "limit": 200}
		if cursor != "" {
			reqBody["cursor"] = cursor
		}
		var resp struct {
			ScheduledShifts []ScheduledShift `json:"scheduled_shifts"`
			Cursor          string           `json:"cursor"`
		}
		if err := c.doJSON(ctx, http.MethodPost, "/v2/labor/scheduled-shifts/search", reqBody, &resp); err != nil {
			return nil, err
		}
		out = append(out, resp.ScheduledShifts...)
		if resp.Cursor == "" {
			break
		}
		cursor = resp.Cursor
	}
	return out, nil
}
