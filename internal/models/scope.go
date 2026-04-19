package models

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// scopeQuerier covers the subset of pgxpool.Pool / pgx.Tx used by
// getOrCreateScopeImpl, so the same logic works for pool and tx callers.
type scopeQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ScopeRow is a denormalized view of one row in the scopes table.
// RoleID and UserID are mutually exclusive; both nil = tenant-wide.
// Used for in-memory access checks via services.Caller.CanSee.
type ScopeRow struct {
	ID     uuid.UUID
	RoleID *uuid.UUID
	UserID *uuid.UUID
}

// ScopeType identifies the kind of scope row used for access control.
// The canonical values match the CHECK constraints on the *_scopes tables.
type ScopeType string

const (
	ScopeTypeTenant   ScopeType = "tenant"
	ScopeTypeRole     ScopeType = "role"
	ScopeTypeUser     ScopeType = "user"
	ScopeTypePlatform ScopeType = "platform" // synthetic, used only for builtin-skill summaries
)

// ScopeValueAll is the scope_value used together with ScopeTypeTenant for
// rows visible to everyone in the tenant.
const ScopeValueAll = "*"

// ScopeFilterIDs builds a SQL fragment matching scope rows visible to the
// given user. Expects a JOIN to the scopes table aliased as `prefix` (or just
// `scopes` when prefix is empty). Bind parameters start at startParam.
//
// Returns the SQL fragment (without surrounding parens) and the args to append.
// services.Caller.ScopeFilterIDs is a thin wrapper around this for callers
// that already have a Caller in hand.
func ScopeFilterIDs(prefix string, startParam int, userID uuid.UUID, roleIDs []uuid.UUID) (string, []any) {
	col := func(name string) string {
		if prefix == "" {
			return name
		}
		return prefix + "." + name
	}

	clauses := []string{
		fmt.Sprintf("(%s IS NULL AND %s IS NULL)", col("role_id"), col("user_id")),
	}
	args := []any{userID}
	clauses = append(clauses, fmt.Sprintf("%s = $%d", col("user_id"), startParam))
	startParam++

	if len(roleIDs) > 0 {
		clauses = append(clauses, fmt.Sprintf("%s = ANY($%d)", col("role_id"), startParam))
		args = append(args, roleIDs)
	}

	return strings.Join(clauses, " OR "), args
}

// PersonalScopeFilterIDs is the narrower variant used for the swipe-stack
// "personal surface" query — excludes tenant-wide rows so the caller only
// sees entities they're personally responsible for.
func PersonalScopeFilterIDs(prefix string, startParam int, userID uuid.UUID, roleIDs []uuid.UUID) (string, []any) {
	col := func(name string) string {
		if prefix == "" {
			return name
		}
		return prefix + "." + name
	}

	args := []any{userID}
	clauses := []string{fmt.Sprintf("%s = $%d", col("user_id"), startParam)}
	startParam++

	if len(roleIDs) > 0 {
		clauses = append(clauses, fmt.Sprintf("%s = ANY($%d)", col("role_id"), startParam))
		args = append(args, roleIDs)
	}

	return strings.Join(clauses, " OR "), args
}

// GetOrCreateScope returns the canonical scope row for the given target.
// Pass roleID for a role scope, userID for a user scope, or both nil for the
// tenant-wide scope. Idempotent — relies on the partial unique indexes on the
// scopes table (scopes_tenant_role_idx, scopes_tenant_user_idx,
// scopes_tenant_wide_idx) to dedupe concurrent inserts.
func GetOrCreateScope(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, roleID, userID *uuid.UUID) (uuid.UUID, error) {
	return getOrCreateScopeImpl(ctx, pool, tenantID, roleID, userID)
}

// GetOrCreateScopeTx is GetOrCreateScope inside a transaction. Use when the
// scope row must be created atomically with its referencing entity.
func GetOrCreateScopeTx(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, roleID, userID *uuid.UUID) (uuid.UUID, error) {
	return getOrCreateScopeImpl(ctx, tx, tenantID, roleID, userID)
}

func getOrCreateScopeImpl(ctx context.Context, q scopeQuerier, tenantID uuid.UUID, roleID, userID *uuid.UUID) (uuid.UUID, error) {
	if roleID != nil && userID != nil {
		return uuid.Nil, errors.New("scope cannot have both role_id and user_id set")
	}

	var where string
	args := []any{tenantID}
	switch {
	case roleID != nil:
		where = "tenant_id = $1 AND role_id = $2"
		args = append(args, *roleID)
	case userID != nil:
		where = "tenant_id = $1 AND user_id = $2"
		args = append(args, *userID)
	default:
		where = "tenant_id = $1 AND role_id IS NULL AND user_id IS NULL"
	}

	var id uuid.UUID
	err := q.QueryRow(ctx, "SELECT id FROM scopes WHERE "+where, args...).Scan(&id)
	if err == nil {
		return id, nil
	}

	err = q.QueryRow(ctx, `
		INSERT INTO scopes (tenant_id, role_id, user_id)
		VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING
		RETURNING id
	`, tenantID, roleID, userID).Scan(&id)
	if err == nil {
		return id, nil
	}

	if err := q.QueryRow(ctx, "SELECT id FROM scopes WHERE "+where, args...).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("get-or-create scope: %w", err)
	}
	return id, nil
}
