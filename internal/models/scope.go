package models

import (
	"fmt"
	"strings"
)

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
		fmt.Sprintf("(%s = 'tenant' AND %s = '*')", st, sv),
	}
	var args []any
	p := startParam

	if len(roleNames) > 0 {
		clauses = append(clauses, fmt.Sprintf("(%s = 'role' AND %s = ANY($%d))", st, sv, p))
		args = append(args, roleNames)
		p++
	}

	if slackUserID != "" {
		clauses = append(clauses, fmt.Sprintf("(%s = 'user' AND %s = $%d)", st, sv, p))
		args = append(args, slackUserID)
	}

	return strings.Join(clauses, "\n\t\t\tOR "), args
}
