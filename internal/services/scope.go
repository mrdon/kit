package services

import (
	"slices"

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
