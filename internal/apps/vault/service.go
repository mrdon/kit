// Package vault implements Kit's password-vault feature.
//
// File map (all in one package, split for the 500-line CLAUDE.md ceiling):
//
//   - service.go (this file): Service struct + the small cross-cutting
//     methods: requireRecentUnlock (step-up auth), tenantSlug,
//     AuditFromRequest, and the toListItem / validateScopesAgainstTenant
//     helpers that both entries.go and unlock.go reach into.
//   - entries.go:    CRUD on app_vault_entries + UpdateScopes.
//   - unlock.go:     Unlock, Register, SelfUnlockTest, Grant, RevokeGrant,
//     DeclinePending, plus the briefing/decision-card
//     helpers (fire*).
//   - validation.go: pubkey/ciphertext/scope validators, scopeDiff,
//     pubkeyFingerprint, dummyHash, nilIfEmpty.
//   - ratelimit.go:  unlockLimiter (per-IP token bucket).
//   - audit.go:      auditCtx + the per-action metadata struct types.
//   - app.go:        apps.App impl, CardSurface interface, CardCreateInput.
//   - tools.go:      agent tool registration + handlers.
//   - mcp.go:        MCP tool registration + handlers.
//   - urls.go:       HTTP route registration + middleware chain.
//   - web.go:        HTTP handler implementations.
package vault

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
)

// Service is the vault's behavior layer. Tools (agent + MCP) and HTTP
// handlers both go through Service so authz, audit, and rate-limiting
// happen in one place.
type Service struct {
	pool *pgxpool.Pool

	// rateLimit is the per-IP token bucket on /api/vault/unlock and
	// /api/vault/self_unlock_test. Plan §"Per-IP rate limit as
	// secondary throttle".
	rateLimit unlockLimiter

	// cards is the card-creation surface, populated via the package-
	// level Configure func from main.go. Used for both admin-targeted
	// decisions (grant requests, failed-unlock alarms) and user-
	// targeted briefings (reset-triggered, access-granted). nil-safe.
	cards CardSurface

	// baseURL is Kit's external origin (e.g. "https://kit.twdata.org"),
	// wired via Configure. Tool handlers prepend it to /{slug}/apps/vault/*
	// paths so the agent's response includes a fully-qualified URL —
	// without it the LLM may hallucinate a host when rendering the path.
	baseURL string
}

// NewService constructs a vault service backed by the given pool.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{
		pool:      pool,
		rateLimit: newUnlockLimiter(perIPCapacity, perIPRefillInterval),
	}
}

// toListItem renders one EntryListItem from a model row. ScopeSummary
// is the role name (post-migration 047 every entry owns to exactly one
// role; visibility flows from role membership, not creator identity).
func toListItem(e models.VaultEntry, _ *services.Caller) EntryListItem {
	username := ""
	if e.Username != nil {
		username = *e.Username
	}
	url := ""
	if e.URL != nil {
		url = *e.URL
	}
	summary := ""
	if e.RoleName != nil {
		summary = *e.RoleName
	}
	return EntryListItem{
		ID:           e.ID,
		Title:        e.Title,
		Username:     username,
		URL:          url,
		Tags:         e.Tags,
		LastViewedAt: e.LastViewedAt,
		ScopeSummary: summary,
		RoleID:       e.RoleID,
		RoleName:     e.RoleName,
	}
}

// validateRoleAgainstTenant verifies role_id is set and belongs to the
// caller's tenant. role_id is required: every entry must own to a
// real role. To make an entry visible to every tenant member, scope
// to the tenant's default_role_id (the 'member' role; see migration
// 002_default_role.sql).
func (s *Service) validateRoleAgainstTenant(ctx context.Context, tenantID uuid.UUID, roleID *uuid.UUID) error {
	if roleID == nil {
		return errors.New("role_id required: pick a role (use the tenant's 'member' role for everyone)")
	}
	var found int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM roles WHERE tenant_id = $1 AND id = $2
	`, tenantID, *roleID).Scan(&found)
	if err != nil {
		return fmt.Errorf("validate role id: %w", err)
	}
	if found == 0 {
		return errors.New("role not in this tenant")
	}
	return nil
}

// requireRecentUnlock enforces step-up auth: the caller must have a
// successful vault.unlock audit event within the last stepUpWindow AND
// must currently be a live, unlocked vault member. The membership join
// matters because a master-password reset (or a teammate revoking the
// caller's grant) leaves stale audit_events behind that would otherwise
// satisfy the step-up window for ~5 minutes after the row was wiped or
// re-pended. Without the join, a recently-unlocked-but-now-reset user
// could still grant or scope-widen during the cooldown.
//
// Returns ErrStepUpRequired on miss. The unlock-event row is the
// source-of-truth timestamp; vault_users gates that the caller can
// actually decrypt anything right now.
func (s *Service) requireRecentUnlock(ctx context.Context, c *services.Caller) error {
	var lastUnlock time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT a.created_at
		FROM audit_events a
		JOIN app_vault_users v
		  ON v.tenant_id = a.tenant_id AND v.user_id = a.actor_user_id
		WHERE a.tenant_id = $1
		  AND a.actor_user_id = $2
		  AND a.action = 'vault.unlock'
		  AND v.pending = FALSE
		  AND v.wrapped_vault_key IS NOT NULL
		  AND v.reset_pending_until IS NULL
		  AND (v.locked_until IS NULL OR v.locked_until <= now())
		ORDER BY a.created_at DESC
		LIMIT 1
	`, c.TenantID, c.UserID).Scan(&lastUnlock)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrStepUpRequired
		}
		return fmt.Errorf("step-up lookup: %w", err)
	}
	if time.Since(lastUnlock) > stepUpWindow {
		return ErrStepUpRequired
	}
	return nil
}

// absURL prefixes a path-only URL with the configured baseURL so the
// agent's tool response includes a fully-qualified link. Slack only
// auto-linkifies absolute URLs; without the host, the LLM tends to
// hallucinate one (e.g. "<slug>.kit.com" instead of the real Kit host).
// Falls through unchanged if baseURL is empty (tests / startup slips).
func (s *Service) absURL(path string) string {
	if s == nil || s.baseURL == "" {
		return path
	}
	return s.baseURL + path
}

// tenantSlug looks up a tenant's URL slug by id. Used by the MCP layer
// to build reveal/add URLs since Caller doesn't carry the slug.
func (s *Service) tenantSlug(ctx context.Context, tenantID uuid.UUID) (string, error) {
	t, err := models.GetTenantByID(ctx, s.pool, tenantID)
	if err != nil {
		return "", err
	}
	if t == nil {
		return "", errors.New("tenant not found")
	}
	return t.Slug, nil
}

// AuditFromRequest constructs the audit context for an HTTP-driven
// action. Convenience wrapper around newAuditCtx.
func (s *Service) AuditFromRequest(c *services.Caller, r *http.Request) auditCtx {
	var actor *uuid.UUID
	if c != nil {
		id := c.UserID
		actor = &id
	}
	tenantID := uuid.Nil
	if c != nil {
		tenantID = c.TenantID
	}
	return newAuditCtx(s.pool, tenantID, actor, r)
}
