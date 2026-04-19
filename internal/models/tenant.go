package models

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Tenant struct {
	ID            uuid.UUID
	SlackTeamID   string
	Name          string
	BotToken      string // encrypted
	Slug          string
	Icon192       []byte
	Icon512       []byte
	BusinessType  *string
	Timezone      string
	SetupComplete bool
	DefaultRoleID *uuid.UUID
	CreatedAt     time.Time
}

// slugValid matches the CHECK constraint in the tenants table. Kept in
// sync with migration 016 and with auth/tenant_path.go.
var slugValid = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// reservedSlugs are workspace slugs that would shadow existing top-level
// routes (e.g. /oauth/callback, /slack/events). The DB enforces this list
// too — keep them in sync.
var reservedSlugs = map[string]struct{}{
	"slack": {}, "mcp": {}, "oauth": {}, "health": {}, "api": {},
	"app": {}, "assets": {}, "admin": {}, "well-known": {},
	"static": {}, "login": {},
}

// IsValidSlug reports whether s matches the slug charset and isn't reserved.
func IsValidSlug(s string) bool {
	if !slugValid.MatchString(s) {
		return false
	}
	_, reserved := reservedSlugs[s]
	return !reserved
}

// SanitizeSlug normalizes a Slack workspace domain into a valid slug.
// Lowercases, replaces invalid chars with hyphens, collapses runs of
// hyphens, trims, caps at 63. If the result is empty or reserved, falls
// back to "ws-<cleaned(teamID)>" (run through the same cleanup so Slack
// team IDs containing underscores also yield a valid slug).
func SanitizeSlug(raw, fallbackTeamID string) string {
	if s := cleanSlug(raw); IsValidSlug(s) {
		return s
	}
	return cleanSlug("ws-" + fallbackTeamID)
}

// cleanSlug applies the slug charset normalization used by SanitizeSlug.
// Kept standalone so both the primary and fallback paths share logic.
func cleanSlug(raw string) string {
	lowered := strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	for _, r := range lowered {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteByte('-')
		}
	}
	s := b.String()
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-")
	if len(s) > 63 {
		s = s[:63]
		s = strings.TrimRight(s, "-")
	}
	return s
}

func UpsertTenant(ctx context.Context, pool *pgxpool.Pool, slackTeamID, name, encryptedToken, slug string, icon192, icon512 []byte) (*Tenant, error) {
	if !IsValidSlug(slug) {
		return nil, fmt.Errorf("invalid slug %q", slug)
	}
	tenant := &Tenant{}
	err := pool.QueryRow(ctx, `
		INSERT INTO tenants (id, slack_team_id, name, bot_token, slug, icon_192, icon_512)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (slack_team_id)
		DO UPDATE SET
			name = EXCLUDED.name,
			bot_token = EXCLUDED.bot_token,
			slug = EXCLUDED.slug,
			icon_192 = COALESCE(EXCLUDED.icon_192, tenants.icon_192),
			icon_512 = COALESCE(EXCLUDED.icon_512, tenants.icon_512)
		RETURNING id, slack_team_id, name, bot_token, slug, icon_192, icon_512, business_type, timezone, setup_complete, default_role_id, created_at
	`, uuid.New(), slackTeamID, name, encryptedToken, slug, icon192, icon512).Scan(
		&tenant.ID, &tenant.SlackTeamID, &tenant.Name, &tenant.BotToken,
		&tenant.Slug, &tenant.Icon192, &tenant.Icon512,
		&tenant.BusinessType, &tenant.Timezone, &tenant.SetupComplete, &tenant.DefaultRoleID, &tenant.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("upserting tenant: %w", err)
	}
	return tenant, nil
}

func GetTenantBySlackTeamID(ctx context.Context, pool *pgxpool.Pool, slackTeamID string) (*Tenant, error) {
	tenant := &Tenant{}
	err := pool.QueryRow(ctx, `
		SELECT id, slack_team_id, name, bot_token, slug, icon_192, icon_512, business_type, timezone, setup_complete, default_role_id, created_at
		FROM tenants WHERE slack_team_id = $1
	`, slackTeamID).Scan(
		&tenant.ID, &tenant.SlackTeamID, &tenant.Name, &tenant.BotToken,
		&tenant.Slug, &tenant.Icon192, &tenant.Icon512,
		&tenant.BusinessType, &tenant.Timezone, &tenant.SetupComplete, &tenant.DefaultRoleID, &tenant.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // not found is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("getting tenant: %w", err)
	}
	return tenant, nil
}

// GetTenantBySlug looks up a tenant by URL slug. Callers MUST pre-validate
// the slug via IsValidSlug to avoid wasted DB round trips and to keep
// enumeration-oracle surface uniform.
func GetTenantBySlug(ctx context.Context, pool *pgxpool.Pool, slug string) (*Tenant, error) {
	tenant := &Tenant{}
	err := pool.QueryRow(ctx, `
		SELECT id, slack_team_id, name, bot_token, slug, icon_192, icon_512, business_type, timezone, setup_complete, default_role_id, created_at
		FROM tenants WHERE slug = $1
	`, slug).Scan(
		&tenant.ID, &tenant.SlackTeamID, &tenant.Name, &tenant.BotToken,
		&tenant.Slug, &tenant.Icon192, &tenant.Icon512,
		&tenant.BusinessType, &tenant.Timezone, &tenant.SetupComplete, &tenant.DefaultRoleID, &tenant.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // not found is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("getting tenant by slug: %w", err)
	}
	return tenant, nil
}

func UpdateTenantSetup(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, businessType, timezone string) error {
	_, err := pool.Exec(ctx, `
		UPDATE tenants SET business_type = $2, timezone = $3, setup_complete = true
		WHERE id = $1
	`, tenantID, businessType, timezone)
	if err != nil {
		return fmt.Errorf("updating tenant setup: %w", err)
	}
	return nil
}

// GetTenantByID finds a tenant by ID.
func GetTenantByID(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (*Tenant, error) {
	tenant := &Tenant{}
	err := pool.QueryRow(ctx, `
		SELECT id, slack_team_id, name, bot_token, slug, icon_192, icon_512, business_type, timezone, setup_complete, default_role_id, created_at
		FROM tenants WHERE id = $1
	`, tenantID).Scan(
		&tenant.ID, &tenant.SlackTeamID, &tenant.Name, &tenant.BotToken,
		&tenant.Slug, &tenant.Icon192, &tenant.Icon512,
		&tenant.BusinessType, &tenant.Timezone, &tenant.SetupComplete, &tenant.DefaultRoleID, &tenant.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // not found is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("getting tenant by id: %w", err)
	}
	return tenant, nil
}

// ListAllTenants returns all tenants.
func ListAllTenants(ctx context.Context, pool *pgxpool.Pool) ([]Tenant, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, slack_team_id, name, bot_token, slug, icon_192, icon_512, business_type, timezone, setup_complete, default_role_id, created_at
		FROM tenants
	`)
	if err != nil {
		return nil, fmt.Errorf("listing tenants: %w", err)
	}
	defer rows.Close()

	var tenants []Tenant
	for rows.Next() {
		var t Tenant
		if err := rows.Scan(&t.ID, &t.SlackTeamID, &t.Name, &t.BotToken,
			&t.Slug, &t.Icon192, &t.Icon512,
			&t.BusinessType, &t.Timezone, &t.SetupComplete, &t.DefaultRoleID, &t.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning tenant: %w", err)
		}
		tenants = append(tenants, t)
	}
	return tenants, nil
}

func SetDefaultRole(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, roleID *uuid.UUID) error {
	_, err := pool.Exec(ctx, `
		UPDATE tenants SET default_role_id = $2 WHERE id = $1
	`, tenantID, roleID)
	if err != nil {
		return fmt.Errorf("setting default role: %w", err)
	}
	return nil
}
