package services

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
)

// ScopeRef is an alias for models.ScopeRow so services callers can build
// access-check inputs without importing models directly. Both RoleID and
// UserID nil means tenant-wide.
type ScopeRef = models.ScopeRow

// CanSee reports whether the caller can see an entity scoped to the given
// scope rows. Empty scopes = invisible (default deny). Admin always wins.
// Tenant-wide scopes (both RoleID and UserID nil) are visible to everyone in
// the tenant.
func (c *Caller) CanSee(scopes []ScopeRef) bool {
	if c.IsAdmin {
		return true
	}
	for _, s := range scopes {
		if s.RoleID == nil && s.UserID == nil {
			return true
		}
		if s.UserID != nil && *s.UserID == c.UserID {
			return true
		}
		if s.RoleID != nil && slices.Contains(c.RoleIDs, *s.RoleID) {
			return true
		}
	}
	return false
}

// ScopeFilterIDs delegates to models.ScopeFilterIDs using the caller's
// UserID and RoleIDs. The returned fragment expects a JOIN to the scopes
// table aliased as `prefix` (or just `scopes` when prefix is empty).
//
// Admins should bypass this filter entirely (caller is responsible for the
// admin branch — this helper does not encode the admin override because the
// SQL shape varies per call site).
func (c *Caller) ScopeFilterIDs(prefix string, startParam int) (string, []any) {
	return models.ScopeFilterIDs(prefix, startParam, c.UserID, c.RoleIDs)
}

// PersonalScopeFilter is the narrower variant used for the swipe-stack
// "personal surface" query — it excludes tenant-wide rows so the caller only
// sees entities they're personally responsible for (assignee or role member).
func (c *Caller) PersonalScopeFilter(prefix string, startParam int) (string, []any) {
	return models.PersonalScopeFilterIDs(prefix, startParam, c.UserID, c.RoleIDs)
}

// ResolveRoleID looks up a role by name within a tenant. Returns ErrNotFound
// (wrapped) when the role doesn't exist.
func ResolveRoleID(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, name string) (uuid.UUID, error) {
	var id uuid.UUID
	err := pool.QueryRow(ctx,
		`SELECT id FROM roles WHERE tenant_id = $1 AND name = $2`,
		tenantID, name).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("role %q: %w", name, ErrNotFound)
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("looking up role %q: %w", name, err)
	}
	return id, nil
}

// resolveScopeTarget translates the agent-tool scope_type/scope_value strings
// into (roleID, userID) pointers suitable for models.CreateRule and friends.
// scopeType "tenant" → (nil, nil); "role" → (role.id, nil); "user" →
// (nil, user.id). Returns an error if the named role/user doesn't exist.
func resolveScopeTarget(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, scopeType, scopeValue string) (*uuid.UUID, *uuid.UUID, error) {
	switch scopeType {
	case string(models.ScopeTypeTenant), "":
		return nil, nil, nil
	case string(models.ScopeTypeRole):
		var id uuid.UUID
		err := pool.QueryRow(ctx,
			`SELECT id FROM roles WHERE tenant_id = $1 AND name = $2`,
			tenantID, scopeValue).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, fmt.Errorf("role %q not found", scopeValue)
		}
		if err != nil {
			return nil, nil, fmt.Errorf("looking up role %q: %w", scopeValue, err)
		}
		return &id, nil, nil
	case string(models.ScopeTypeUser):
		var id uuid.UUID
		err := pool.QueryRow(ctx,
			`SELECT id FROM users WHERE tenant_id = $1 AND slack_user_id = $2`,
			tenantID, scopeValue).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, fmt.Errorf("user %q not found", scopeValue)
		}
		if err != nil {
			return nil, nil, fmt.Errorf("looking up user %q: %w", scopeValue, err)
		}
		return nil, &id, nil
	default:
		return nil, nil, fmt.Errorf("unknown scope type %q", scopeType)
	}
}
