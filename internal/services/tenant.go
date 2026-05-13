package services

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/models"
)

// TenantTools defines the shared tool metadata for tenant operations.
var TenantTools = []ToolMeta{
	{Name: "update_tenant", Description: "Update the organization's business info and mark setup as complete.", Schema: propsReq(map[string]any{
		"business_type":  field("string", "Type of business (e.g., 'brewery', 'nonprofit')"),
		"timezone":       field("string", "IANA timezone (e.g., 'America/Denver')"),
		"setup_complete": map[string]any{"type": "boolean", "description": "Mark setup as complete"},
	}, "business_type"), AdminOnly: true},
}

// SlackTeamInfoFetcher abstracts the slack.Client.TeamInfo call so the
// services package doesn't depend on the slack package directly.
// Implementations should call slack's team.info API with the given bot
// token and return the workspace subdomain.
type SlackTeamInfoFetcher func(ctx context.Context, botToken string) (domain string, err error)

// TenantService handles tenant operations with authorization.
type TenantService struct {
	pool     *pgxpool.Pool
	enc      *crypto.Encryptor
	teamInfo SlackTeamInfoFetcher // nil disables EnsureSlackDomain backfill
}

// Update updates tenant business info. Admin only.
func (s *TenantService) Update(ctx context.Context, c *Caller, businessType, timezone string) error {
	if !c.IsAdmin {
		return ErrForbidden
	}
	if timezone == "" {
		timezone = "UTC"
	}
	return models.UpdateTenantSetup(ctx, s.pool, c.TenantID, businessType, timezone)
}

// EnsureSlackDomain returns the tenant's Slack workspace subdomain,
// fetching and persisting it via team.info if not already stored.
// Tenants installed before slack_team_domain existed land here with an
// empty value; one team.info call backfills the row so subsequent calls
// are pure DB reads.
//
// Returns "" (no error) if the fetcher is unwired, the bot token can't
// be decrypted, or Slack's response lacks a domain — the caller should
// proceed with the empty value, which downstream URL builders treat as
// "fall back to slack.com without workspace pinning".
func (s *TenantService) EnsureSlackDomain(ctx context.Context, tenant *models.Tenant) string {
	if tenant.SlackTeamDomain != "" {
		return tenant.SlackTeamDomain
	}
	if s.teamInfo == nil || s.enc == nil {
		return ""
	}
	botToken, err := s.enc.Decrypt(tenant.BotToken)
	if err != nil {
		slog.Warn("tenant: decrypting bot token for domain backfill", "tenant_id", tenant.ID, "error", err)
		return ""
	}
	domain, err := s.teamInfo(ctx, botToken)
	if err != nil || domain == "" {
		if err != nil {
			slog.Warn("tenant: team.info during domain backfill", "tenant_id", tenant.ID, "error", err)
		}
		return ""
	}
	if err := models.SetTenantSlackDomain(ctx, s.pool, tenant.ID, domain); err != nil {
		slog.Warn("tenant: persisting backfilled domain", "tenant_id", tenant.ID, "error", err)
		// Fall through — return the value for this request even if the
		// write failed; the next call will retry.
	}
	tenant.SlackTeamDomain = domain
	return domain
}
