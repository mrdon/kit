package models

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

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

// ScopeFilter builds a SQL WHERE clause fragment for scope-based access control.
// It matches rows where the scope is tenant-wide, matches one of the user's roles,
// or matches the specific user. The prefix is the table alias or empty string for
// inline scope columns (e.g. "ss" for "ss.scope_type" or "" for "scope_type").
// startParam is the next available $N placeholder index.
// Returns the SQL fragment (without surrounding parens) and the args to append.
func ScopeFilter(prefix string, startParam int, slackUserID string, roleNames []string) (string, []any) {
	col := func(name string) string {
		if prefix == "" {
			return name
		}
		return prefix + "." + name
	}

	st := col("scope_type")
	sv := col("scope_value")

	clauses := []string{
		fmt.Sprintf("(%s = '%s' AND %s = '%s')", st, ScopeTypeTenant, sv, ScopeValueAll),
	}
	var args []any
	p := startParam

	if len(roleNames) > 0 {
		clauses = append(clauses, fmt.Sprintf("(%s = '%s' AND %s = ANY($%d))", st, ScopeTypeRole, sv, p))
		args = append(args, roleNames)
		p++
	}

	if slackUserID != "" {
		clauses = append(clauses, fmt.Sprintf("(%s = '%s' AND %s = $%d)", st, ScopeTypeUser, sv, p))
		args = append(args, slackUserID)
	}

	return strings.Join(clauses, "\n\t\t\tOR "), args
}

// GetOrCreateScope returns the canonical scope row for the given target.
// Pass roleID for a role scope, userID for a user scope, or both nil for the
// tenant-wide scope. Idempotent — relies on the partial unique indexes on the
// scopes table (scopes_tenant_role_idx, scopes_tenant_user_idx,
// scopes_tenant_wide_idx) to dedupe concurrent inserts.
func GetOrCreateScope(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, roleID, userID *uuid.UUID) (uuid.UUID, error) {
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
	err := pool.QueryRow(ctx, "SELECT id FROM scopes WHERE "+where, args...).Scan(&id)
	if err == nil {
		return id, nil
	}

	err = pool.QueryRow(ctx, `
		INSERT INTO scopes (tenant_id, role_id, user_id)
		VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING
		RETURNING id
	`, tenantID, roleID, userID).Scan(&id)
	if err == nil {
		return id, nil
	}

	if err := pool.QueryRow(ctx, "SELECT id FROM scopes WHERE "+where, args...).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("get-or-create scope: %w", err)
	}
	return id, nil
}
