package models

import (
	"fmt"
	"strings"
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
