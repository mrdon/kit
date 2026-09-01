package square

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
)

// Provider / AuthType identify the Square integration row in the
// integrations substrate. Square is tenant-scoped (one connection per
// workspace), so the integration's user_id is always NULL.
const (
	Provider = "square"
	AuthType = "oauth2"
)

// ErrNotConfigured is returned when the tenant has no Square integration
// row. Callers translate it into a setup hint.
var ErrNotConfigured = errors.New("square integration not configured")

// ErrNotReady is returned when Configure hasn't supplied app credentials —
// without them token refresh (and therefore any long-lived use) can't work.
var ErrNotReady = errors.New("square app credentials not configured")

// ErrMissingScope means the tenant's token authenticates fine but was
// granted without the permission this call needs. Distinct from
// ErrNotConfigured: the fix is reconnecting with a wider-scoped token, not
// connecting for the first time. Deliberately NOT swallowed by the
// scheduled tasks — it is actionable and belongs in the job's last_error.
var ErrMissingScope = errors.New("square token is missing a required permission")

// LoadClient builds an authenticated Square client for the tenant. It reads
// and decrypts the stored access + refresh tokens and wires a refresh hook
// so an expired access token is transparently renewed and persisted on the
// first 401. Returns ErrNotConfigured when the tenant hasn't connected.
func (a *App) LoadClient(ctx context.Context, tenantID uuid.UUID) (*Client, error) {
	if a.enc == nil {
		return nil, ErrNotReady
	}

	integ, err := models.GetIntegration(ctx, a.pool, tenantID, Provider, AuthType, nil)
	if err != nil {
		return nil, fmt.Errorf("loading square integration: %w", err)
	}
	if integ == nil {
		return nil, ErrNotConfigured
	}

	primaryEnc, secondaryEnc, err := models.GetIntegrationTokens(ctx, a.pool, tenantID, integ.ID)
	if err != nil {
		return nil, fmt.Errorf("loading square tokens: %w", err)
	}
	if primaryEnc == "" {
		return nil, fmt.Errorf("square integration %s has no access token", integ.ID)
	}
	accessToken, err := a.enc.Decrypt(primaryEnc)
	if err != nil {
		return nil, fmt.Errorf("decrypting square access token: %w", err)
	}
	refreshToken := ""
	if secondaryEnc != "" {
		if refreshToken, err = a.enc.Decrypt(secondaryEnc); err != nil {
			return nil, fmt.Errorf("decrypting square refresh token: %w", err)
		}
	}

	c := &Client{apiBase: a.apiBase, accessToken: accessToken}
	// Auto-refresh needs both a refresh token and the app's OAuth
	// credentials. A non-expiring personal access token (no refresh token)
	// is used as-is, which is the simplest single-team setup.
	if refreshToken != "" && a.clientID != "" && a.clientSecret != "" {
		c.refreshAndPersist = a.refreshHook(tenantID, integ.ID, refreshToken, integ.Config)
	}
	return c, nil
}

// refreshHook returns a closure that exchanges the refresh token for a new
// access token, persists both, and returns the new access token. It closes
// over the stored refresh token; Square's code flow returns the same
// refresh token on refresh, but we persist whatever comes back.
func (a *App) refreshHook(tenantID, integrationID uuid.UUID, refreshToken string, config map[string]any) func(ctx context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		tok, err := a.refreshToken(ctx, refreshToken)
		if err != nil {
			return "", err
		}
		primaryEnc, err := a.enc.Encrypt(tok.AccessToken)
		if err != nil {
			return "", fmt.Errorf("encrypting refreshed access token: %w", err)
		}
		newRefresh := tok.RefreshToken
		if newRefresh == "" {
			newRefresh = refreshToken
		}
		secondaryEnc, err := a.enc.Encrypt(newRefresh)
		if err != nil {
			return "", fmt.Errorf("encrypting refreshed refresh token: %w", err)
		}
		if config == nil {
			config = map[string]any{}
		}
		if tok.MerchantID != "" {
			config["merchant_id"] = tok.MerchantID
		}
		if tok.ExpiresAt != "" {
			config["expires_at"] = tok.ExpiresAt
		}
		if err := models.UpdateIntegrationTokens(ctx, a.pool, tenantID, integrationID, primaryEnc, secondaryEnc, config); err != nil {
			return "", err
		}
		return tok.AccessToken, nil
	}
}

// tokenResponse mirrors the Square ObtainToken response.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    string `json:"expires_at"`
	MerchantID   string `json:"merchant_id"`
	TokenType    string `json:"token_type"`
}

// refreshToken exchanges a refresh token for a fresh access token. Square's
// ObtainToken accepts a JSON body. Uses a bare http client (not the Client
// wrapper) since there's no bearer auth on this endpoint.
func (a *App) refreshToken(ctx context.Context, refreshToken string) (*tokenResponse, error) {
	reqBody := map[string]string{
		"client_id":     a.clientID,
		"client_secret": a.clientSecret,
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
	}
	c := &Client{apiBase: a.apiBase}
	var out tokenResponse
	// doOnce sets a bogus bearer header (empty token) but the token
	// endpoint ignores Authorization; the client_id/secret in the body
	// authenticate the call.
	if err := c.doOnce(ctx, http.MethodPost, "/oauth2/token", reqBody, &out); err != nil {
		return nil, fmt.Errorf("square token refresh: %w", err)
	}
	if out.AccessToken == "" {
		return nil, errors.New("square token refresh returned no access_token")
	}
	return &out, nil
}

// EnrichedShift is a published scheduled shift decorated with resolved
// team-member and location display names, ready for human-readable output.
type EnrichedShift struct {
	ShiftID  string
	StartAt  string
	EndAt    string
	Timezone string
	// TeamMemberID is Square's stable id for the person. Names are mutable
	// and not unique, so anything that maps a shift to an identity outside
	// Square keys on this rather than on Member.
	TeamMemberID string
	Member       string // full display name, e.g. "Alice Ng"
	// MemberFirst is the given name ("Alice"), falling back to Member. Used
	// for the informal calendar-event title.
	MemberFirst string
	Location    string
	Notes       string
}

// ListPublishedShifts pulls PUBLISHED shifts in [start, end) and decorates
// them with team-member and location names. This is the read primitive the
// verification tool and (later) the calendar sync both build on.
func (a *App) ListPublishedShifts(ctx context.Context, tenantID uuid.UUID, start, end time.Time) ([]EnrichedShift, error) {
	c, err := a.LoadClient(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	locations, err := c.ListLocations(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing locations: %w", err)
	}
	locByID := make(map[string]string, len(locations))
	var locIDs []string
	for _, l := range locations {
		locByID[l.ID] = l.Name
		locIDs = append(locIDs, l.ID)
	}
	members, err := c.SearchTeamMembers(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing team members: %w", err)
	}
	shifts, err := c.SearchPublishedShifts(ctx, locIDs, start, end)
	if err != nil {
		return nil, fmt.Errorf("searching shifts: %w", err)
	}

	out := make([]EnrichedShift, 0, len(shifts))
	for _, s := range shifts {
		d := s.PublishedShiftDetails
		if d == nil || d.IsDeleted {
			continue
		}
		member := d.TeamMemberID
		memberFirst := member
		if m, ok := members[d.TeamMemberID]; ok {
			member = m.DisplayName()
			memberFirst = m.GivenName
			if memberFirst == "" {
				memberFirst = member
			}
		} else if d.TeamMemberID == "" {
			member = "(open shift)"
			memberFirst = member
		}
		location := d.LocationID
		if name, ok := locByID[d.LocationID]; ok {
			location = name
		}
		out = append(out, EnrichedShift{
			ShiftID:      s.ID,
			TeamMemberID: d.TeamMemberID,
			StartAt:      d.StartAt,
			EndAt:        d.EndAt,
			Timezone:     d.Timezone,
			Member:       member,
			MemberFirst:  memberFirst,
			Location:     location,
			Notes:        d.Notes,
		})
	}
	return out, nil
}
