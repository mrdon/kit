package events

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultTimezone is the fallback when a tenant has not configured one. Kit is
// multi-tenant, but the venue's own zone is the meaningful default for an
// events app and the only tenant today is in Colorado.
const DefaultTimezone = "America/Denver"

// Settings is the per-tenant configuration row. A tenant with no row gets the
// zero value with defaults filled in, so the app is usable before anyone
// visits the admin page.
type Settings struct {
	TenantID uuid.UUID `json:"tenant_id"`
	// CalendarID is chosen from a picker on the app's admin page. Empty means
	// calendar sync is a no-op -- not an error, just not configured yet.
	CalendarID string `json:"calendar_id"`
	Timezone   string `json:"timezone"`
	// PublicURLTemplate contains a {slug} placeholder, e.g.
	// "https://example.com/events/{slug}". Canonical URLs are derived from it
	// at read time so changing the domain never requires a data migration.
	PublicURLTemplate string    `json:"public_url_template"`
	FeedToken         string    `json:"-"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// CalendarConfigured reports whether the sync has somewhere to write.
func (s Settings) CalendarConfigured() bool { return strings.TrimSpace(s.CalendarID) != "" }

// CanonicalURL renders the public URL for a slug, or "" when no template is
// configured. Callers treat "" as "the website has no page for this yet".
func (s Settings) CanonicalURL(slug string) string {
	tpl := strings.TrimSpace(s.PublicURLTemplate)
	if tpl == "" || slug == "" {
		return ""
	}
	return strings.ReplaceAll(tpl, "{slug}", slug)
}

// Loc resolves the tenant's default zone, falling back to UTC.
func (s Settings) Loc() *time.Location {
	tz := s.Timezone
	if tz == "" {
		tz = DefaultTimezone
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC
	}
	return loc
}

func getSettings(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (Settings, error) {
	var s Settings
	err := pool.QueryRow(ctx, `
		SELECT tenant_id, calendar_id, timezone, public_url_template, feed_token, updated_at
		FROM app_event_settings WHERE tenant_id = $1`, tenantID).
		Scan(&s.TenantID, &s.CalendarID, &s.Timezone, &s.PublicURLTemplate, &s.FeedToken, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// Unconfigured is a normal state, not an error.
		return Settings{TenantID: tenantID, Timezone: DefaultTimezone}, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("loading event settings: %w", err)
	}
	return s, nil
}

func upsertSettings(ctx context.Context, pool *pgxpool.Pool, s Settings) (Settings, error) {
	tz := s.Timezone
	if tz == "" {
		tz = DefaultTimezone
	}
	var out Settings
	err := pool.QueryRow(ctx, `
		INSERT INTO app_event_settings (tenant_id, calendar_id, timezone, public_url_template, feed_token, updated_at)
		VALUES ($1, $2, $3, $4, $5, now())
		ON CONFLICT (tenant_id) DO UPDATE SET
			calendar_id = EXCLUDED.calendar_id,
			timezone = EXCLUDED.timezone,
			public_url_template = EXCLUDED.public_url_template,
			feed_token = EXCLUDED.feed_token,
			updated_at = now()
		RETURNING tenant_id, calendar_id, timezone, public_url_template, feed_token, updated_at`,
		s.TenantID, strings.TrimSpace(s.CalendarID), tz,
		strings.TrimSpace(s.PublicURLTemplate), s.FeedToken).
		Scan(&out.TenantID, &out.CalendarID, &out.Timezone, &out.PublicURLTemplate, &out.FeedToken, &out.UpdatedAt)
	if err != nil {
		return Settings{}, fmt.Errorf("saving event settings: %w", err)
	}
	return out, nil
}

// NewFeedToken mints a bearer token for the build-time feed. URL-safe so it
// can live in a build environment variable without escaping.
func NewFeedToken() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generating feed token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf[:]), nil
}
