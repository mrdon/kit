package googlecalendar

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
)

// Provider / AuthType identify the Google Calendar integration row. It's
// tenant-scoped (one connection per workspace), so user_id is always NULL.
const (
	Provider = "google_calendar"
	AuthType = "service_account"
)

// ErrNotConfigured is returned when the tenant has no Google Calendar
// integration row.
var ErrNotConfigured = errors.New("google calendar integration not configured")

// ErrNotReady is returned before Configure has supplied the encryptor.
var ErrNotReady = errors.New("google calendar app not configured")

// LoadClient builds an authenticated Calendar client for the tenant and
// returns it alongside the configured target calendar id. It decrypts the
// service-account key and wires a mint hook so access tokens are obtained
// lazily and refreshed on expiry/401.
//
// The returned calendar id is the integration-level default, which belongs to
// whichever feature was set up first (today: the Square shift sync). An app
// that targets its own calendar should call LoadClientOnly instead — every
// write method takes calendarID per call, so the default is not load-bearing.
func (a *App) LoadClient(ctx context.Context, tenantID uuid.UUID) (*Client, string, error) {
	c, integ, err := a.loadClient(ctx, tenantID)
	if err != nil {
		return nil, "", err
	}
	calendarID, _ := integ.Config["calendar_id"].(string)
	if calendarID == "" {
		return nil, "", fmt.Errorf("google calendar integration %s missing calendar_id", integ.ID)
	}
	return c, calendarID, nil
}

// LoadClientOnly builds the authenticated client without requiring the
// integration to carry a calendar_id. Use it when the caller supplies its own
// calendar (see LoadClient's note); the credential is shared, the target is not.
func (a *App) LoadClientOnly(ctx context.Context, tenantID uuid.UUID) (*Client, error) {
	c, _, err := a.loadClient(ctx, tenantID)
	return c, err
}

// loadClient does the credential half: fetch the integration row, decrypt the
// service-account key, and wire the token-mint hook. It deliberately does not
// look at calendar_id — that is the caller's concern.
func (a *App) loadClient(ctx context.Context, tenantID uuid.UUID) (*Client, *models.Integration, error) {
	if a.enc == nil {
		return nil, nil, ErrNotReady
	}
	integ, err := models.GetIntegration(ctx, a.pool, tenantID, Provider, AuthType, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("loading google calendar integration: %w", err)
	}
	if integ == nil {
		return nil, nil, ErrNotConfigured
	}

	primaryEnc, _, err := models.GetIntegrationTokens(ctx, a.pool, tenantID, integ.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("loading google calendar key: %w", err)
	}
	if primaryEnc == "" {
		return nil, nil, fmt.Errorf("google calendar integration %s has no service account key", integ.ID)
	}
	keyJSON, err := a.enc.Decrypt(primaryEnc)
	if err != nil {
		return nil, nil, fmt.Errorf("decrypting service account key: %w", err)
	}
	key, err := parseServiceAccountKey(keyJSON)
	if err != nil {
		return nil, nil, err
	}

	c := &Client{mint: func(ctx context.Context) (string, time.Time, error) {
		return mintAccessToken(ctx, key)
	}}
	return c, integ, nil
}

// CheckWriteAccess verifies the service account can write to the configured
// calendar by inserting a probe event and immediately deleting it. Returns a
// human-readable summary. This is the connection verifier for deploy-time.
func (a *App) CheckWriteAccess(ctx context.Context, tenantID uuid.UUID) (string, error) {
	c, calendarID, err := a.LoadClient(ctx, tenantID)
	if err != nil {
		return "", err
	}
	start := time.Now().Add(24 * time.Hour).Truncate(time.Hour)
	probe := &Event{
		ID:          probeEventID(tenantID, start),
		Summary:     "Kit connection check (safe to ignore)",
		Description: "Written by Kit to verify calendar write access; deleted immediately.",
		Start:       &EventDateTime{DateTime: start.UTC().Format(time.RFC3339), TimeZone: "UTC"},
		End:         &EventDateTime{DateTime: start.Add(30 * time.Minute).UTC().Format(time.RFC3339), TimeZone: "UTC"},
		ExtendedProperties: &ExtendedProperties{Private: map[string]string{
			"source": "kit-check",
		}},
	}
	if _, err := c.UpsertEvent(ctx, calendarID, probe); err != nil {
		return "", fmt.Errorf("probe event write failed: %w", err)
	}
	if err := c.DeleteEvent(ctx, calendarID, probe.ID); err != nil {
		return fmt.Sprintf("Wrote a probe event but failed to clean it up (%v) — write access works; you may want to delete 'Kit connection check' from the calendar.", err), nil
	}
	return "Google Calendar write access confirmed (calendar " + calendarID + ").", nil
}

// ServiceAccountEmail returns the address a calendar must be shared with for
// this tenant's integration to write to it.
//
// Not a secret — it is an email address you type into Google Calendar's
// sharing dialog, and the private key it sits beside is never returned. It is
// exposed because the alternative is an admin being told to "share the
// calendar with the service account" and having no way to discover which
// address that is.
func (a *App) ServiceAccountEmail(ctx context.Context, tenantID uuid.UUID) (string, error) {
	if a.enc == nil {
		return "", ErrNotReady
	}
	integ, err := models.GetIntegration(ctx, a.pool, tenantID, Provider, AuthType, nil)
	if err != nil {
		return "", fmt.Errorf("loading google calendar integration: %w", err)
	}
	if integ == nil {
		return "", ErrNotConfigured
	}
	primaryEnc, _, err := models.GetIntegrationTokens(ctx, a.pool, tenantID, integ.ID)
	if err != nil || primaryEnc == "" {
		return "", ErrNotConfigured
	}
	keyJSON, err := a.enc.Decrypt(primaryEnc)
	if err != nil {
		return "", fmt.Errorf("decrypting service account key: %w", err)
	}
	key, err := parseServiceAccountKey(keyJSON)
	if err != nil {
		return "", err
	}
	return key.ClientEmail, nil
}
