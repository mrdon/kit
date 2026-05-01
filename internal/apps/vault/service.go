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
}

// NewService constructs a vault service backed by the given pool.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{
		pool:      pool,
		rateLimit: newUnlockLimiter(perIPCapacity, perIPRefillInterval),
	}
}

// toListItem renders one EntryListItem from a model row + the calling
// caller. ScopeSummary is filled in by ListEntries (it needs a batch
// lookup); IsOwner is derived directly.
func toListItem(e models.VaultEntry, c *services.Caller) EntryListItem {
	username := ""
	if e.Username != nil {
		username = *e.Username
	}
	url := ""
	if e.URL != nil {
		url = *e.URL
	}
	return EntryListItem{
		ID:           e.ID,
		Title:        e.Title,
		Username:     username,
		URL:          url,
		Tags:         e.Tags,
		LastViewedAt: e.LastViewedAt,
		IsOwner:      e.OwnerUserID == c.UserID,
	}
}

// validateScopesAgainstTenant verifies every user/role principal id in
// the scope set actually belongs to the caller's tenant. Without this,
// a malicious caller could write scope rows referencing a uuid from a
// different tenant — those rows would never match the scope filter
// (which also checks tenant_id), but they pollute the table with dead
// refs.
func (s *Service) validateScopesAgainstTenant(ctx context.Context, tenantID uuid.UUID, scopes []models.VaultEntryScope) error {
	var userIDs, roleIDs []uuid.UUID
	for _, sc := range scopes {
		if sc.ScopeID == nil {
			continue
		}
		switch sc.ScopeKind {
		case "user":
			userIDs = append(userIDs, *sc.ScopeID)
		case "role":
			roleIDs = append(roleIDs, *sc.ScopeID)
		}
	}
	if len(userIDs) > 0 {
		var found int
		err := s.pool.QueryRow(ctx, `
			SELECT count(*) FROM users WHERE tenant_id = $1 AND id = ANY($2)
		`, tenantID, userIDs).Scan(&found)
		if err != nil {
			return fmt.Errorf("validate user scopes: %w", err)
		}
		if found != len(userIDs) {
			return errors.New("scope references a user not in this tenant")
		}
	}
	if len(roleIDs) > 0 {
		var found int
		err := s.pool.QueryRow(ctx, `
			SELECT count(*) FROM roles WHERE tenant_id = $1 AND id = ANY($2)
		`, tenantID, roleIDs).Scan(&found)
		if err != nil {
			return fmt.Errorf("validate role scopes: %w", err)
		}
		if found != len(roleIDs) {
			return errors.New("scope references a role not in this tenant")
		}
	}
	return nil
}

// requireRecentUnlock enforces step-up auth: the caller must have a
// successful vault.unlock audit event within the last stepUpWindow.
// Returns ErrStepUpRequired if not. Implemented as an audit_events
// query so the unlock record is the source of truth — no extra column
// to keep in sync. The vault.unlock event is fail-closed-written by
// Unlock so a transient audit-write failure doesn't lock out a user.
func (s *Service) requireRecentUnlock(ctx context.Context, c *services.Caller) error {
	var lastUnlock time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT created_at FROM audit_events
		WHERE tenant_id = $1 AND actor_user_id = $2 AND action = 'vault.unlock'
		ORDER BY created_at DESC
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
