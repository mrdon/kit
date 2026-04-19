package services

import (
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
)

// ScopeRef is a denormalized view of one row in the scopes table.
// RoleID and UserID are mutually exclusive; both nil = tenant-wide.
type ScopeRef struct {
	ID     uuid.UUID
	RoleID *uuid.UUID
	UserID *uuid.UUID
}

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
			return true // tenant-wide
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

// ScopeFilterIDs returns a SQL fragment + args that match scope_ids the caller
// can see. The fragment expects a JOIN to the scopes table aliased as `prefix`
// (or just `scopes` when prefix is empty). Bind parameters start at startParam.
//
// Example use:
//
//	frag, args := caller.ScopeFilterIDs("s", 2)
//	// "(s.role_id IS NULL AND s.user_id IS NULL) OR s.user_id = $2 OR s.role_id = ANY($3)"
//	pool.Query(ctx, "SELECT ... FROM skill_scopes ss JOIN scopes s ON s.id=ss.scope_id WHERE ss.tenant_id=$1 AND ("+frag+")", append([]any{tenantID}, args...)...)
//
// Admins should bypass this filter entirely (caller is responsible for the
// admin branch — this helper does not encode the admin override because the
// SQL shape varies per call site).
func (c *Caller) ScopeFilterIDs(prefix string, startParam int) (string, []any) {
	col := func(name string) string {
		if prefix == "" {
			return name
		}
		return prefix + "." + name
	}

	var ors []string
	var args []any
	n := startParam

	ors = append(ors, fmt.Sprintf("(%s IS NULL AND %s IS NULL)", col("role_id"), col("user_id")))

	ors = append(ors, fmt.Sprintf("%s = $%d", col("user_id"), n))
	args = append(args, c.UserID)
	n++

	if len(c.RoleIDs) > 0 {
		ors = append(ors, fmt.Sprintf("%s = ANY($%d)", col("role_id"), n))
		args = append(args, c.RoleIDs)
	}

	return "(" + strings.Join(ors, " OR ") + ")", args
}

// PersonalScopeFilter is the narrower variant used for the swipe-stack
// "personal surface" query — it excludes tenant-wide rows so the caller only
// sees entities they're personally responsible for (assignee or role member).
// Used by internal/apps/todo/cardprovider.go to keep the stack focused.
func (c *Caller) PersonalScopeFilter(prefix string, startParam int) (string, []any) {
	col := func(name string) string {
		if prefix == "" {
			return name
		}
		return prefix + "." + name
	}

	var ors []string
	var args []any
	n := startParam

	ors = append(ors, fmt.Sprintf("%s = $%d", col("user_id"), n))
	args = append(args, c.UserID)
	n++

	if len(c.RoleIDs) > 0 {
		ors = append(ors, fmt.Sprintf("%s = ANY($%d)", col("role_id"), n))
		args = append(args, c.RoleIDs)
	}

	return "(" + strings.Join(ors, " OR ") + ")", args
}
