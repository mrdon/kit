package events

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
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
	PublicURLTemplate string `json:"public_url_template"`
	FeedToken         string `json:"-"`
	// SiteBuildHookURL carries its own secret in the path, so it is never
	// serialised back to the browser -- the UI only learns whether one is set.
	SiteBuildHookURL string `json:"-"`
	// NoticeChannelID is where the daily shift notice is posted. Empty means
	// notices are off -- there is no safe channel to guess at.
	NoticeChannelID   string     `json:"notice_channel_id"`
	NoticeChannelName string     `json:"notice_channel_name"`
	SiteBuiltAt       *time.Time `json:"site_built_at,omitempty"`
	SiteBuiltBy       string     `json:"site_built_by,omitempty"`
	UpdatedAt         time.Time  `json:"updated_at"`
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

// SiteBaseURL is the website's origin, derived from the same template the
// canonical URLs come from so there is no second place to configure a domain.
//
// It exists for the published ICS addresses. Those live on the brewery's own
// site rather than on Kit -- a chamber's calendar platform cannot send a
// bearer token, so what they subscribe to has to be the token-free copy the
// build republishes. Empty when no template is set, which the admin page
// reports rather than rendering three dead links.
func (s Settings) SiteBaseURL() string {
	tpl := strings.TrimSpace(s.PublicURLTemplate)
	if tpl == "" {
		return ""
	}
	u, err := url.Parse(tpl)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
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
		SELECT tenant_id, calendar_id, timezone, public_url_template, feed_token,
		       site_build_hook_url, site_built_at, site_built_by, updated_at,
		       notice_channel_id, notice_channel_name
		FROM app_event_settings WHERE tenant_id = $1`, tenantID).
		Scan(&s.TenantID, &s.CalendarID, &s.Timezone, &s.PublicURLTemplate, &s.FeedToken,
			&s.SiteBuildHookURL, &s.SiteBuiltAt, &s.SiteBuiltBy, &s.UpdatedAt,
			&s.NoticeChannelID, &s.NoticeChannelName)
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
		INSERT INTO app_event_settings (tenant_id, calendar_id, timezone, public_url_template, feed_token, site_build_hook_url, notice_channel_id, notice_channel_name, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
		ON CONFLICT (tenant_id) DO UPDATE SET
			calendar_id = EXCLUDED.calendar_id,
			timezone = EXCLUDED.timezone,
			public_url_template = EXCLUDED.public_url_template,
			feed_token = EXCLUDED.feed_token,
			site_build_hook_url = EXCLUDED.site_build_hook_url,
			notice_channel_id = EXCLUDED.notice_channel_id,
			notice_channel_name = EXCLUDED.notice_channel_name,
			updated_at = now()
		RETURNING tenant_id, calendar_id, timezone, public_url_template, feed_token,
		          site_build_hook_url, site_built_at, site_built_by, updated_at,
		          notice_channel_id, notice_channel_name`,
		s.TenantID, strings.TrimSpace(s.CalendarID), tz,
		strings.TrimSpace(s.PublicURLTemplate), s.FeedToken, strings.TrimSpace(s.SiteBuildHookURL),
		strings.TrimSpace(s.NoticeChannelID), strings.TrimSpace(s.NoticeChannelName)).
		Scan(&out.TenantID, &out.CalendarID, &out.Timezone, &out.PublicURLTemplate, &out.FeedToken,
			&out.SiteBuildHookURL, &out.SiteBuiltAt, &out.SiteBuiltBy, &out.UpdatedAt,
			&out.NoticeChannelID, &out.NoticeChannelName)
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

// setSiteBuilt stamps a completed website build. Separate from upsertSettings
// so recording a build can never clobber a concurrent settings edit.
func setSiteBuilt(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, at time.Time, by string) error {
	_, err := pool.Exec(ctx, `
		UPDATE app_event_settings
		SET site_built_at = $2, site_built_by = $3
		WHERE tenant_id = $1`, tenantID, at, by)
	if err != nil {
		return fmt.Errorf("recording website build: %w", err)
	}
	return nil
}
