package services

import (
	"context"
	"errors"
	"fmt"
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

// ErrSlackDomainUnavailable is returned by EnsureSlackDomain when the
// workspace subdomain can't be resolved — either the service is unwired
// (test/dev) or the team.info call failed. Callers that need the domain
// to render correct UX should fail loud rather than silently degrade.
var ErrSlackDomainUnavailable = errors.New("slack team domain unavailable")

// EnsureSlackDomain returns the tenant's Slack workspace subdomain,
// fetching and persisting it via team.info if not already stored.
// Tenants installed before slack_team_domain existed land here empty;
// one team.info call backfills the row so subsequent reads are pure DB.
//
// Returns ErrSlackDomainUnavailable if the value can't be produced
// (encryptor unwired, decrypt fails, team.info fails, or Slack returns
// an empty domain). Caller decides whether to 500 or degrade.
func (s *TenantService) EnsureSlackDomain(ctx context.Context, tenant *models.Tenant) (string, error) {
	if tenant.SlackTeamDomain != "" {
		return tenant.SlackTeamDomain, nil
	}
	if s.teamInfo == nil || s.enc == nil {
		return "", ErrSlackDomainUnavailable
	}
	botToken, err := s.enc.Decrypt(tenant.BotToken)
	if err != nil {
		return "", fmt.Errorf("decrypting bot token: %w", err)
	}
	domain, err := s.teamInfo(ctx, botToken)
	if err != nil {
		return "", fmt.Errorf("team.info: %w", err)
	}
	if domain == "" {
		return "", ErrSlackDomainUnavailable
	}
	if err := models.SetTenantSlackDomain(ctx, s.pool, tenant.ID, domain); err != nil {
		slog.Warn("tenant: persisting backfilled domain", "tenant_id", tenant.ID, "error", err)
		// Return the value anyway — we have it for this request even if
		// the write failed; the next call will retry the backfill.
	}
	tenant.SlackTeamDomain = domain
	return domain, nil
}
