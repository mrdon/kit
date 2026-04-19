package builder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mrdon/kit/internal/apps/builder/runtime"
)

// buildFindQuery composes the SELECT for Find / FindOne. Pulled out so Find
// itself stays in the streaming/scanning role and under the function-size cap.
func buildFindQuery(scope Scope, filter map[string]any, sort []any, limit, skip int) (string, []any, error) {
	where, params, err := buildScopedWhere(scope, filter)
	if err != nil {
		return "", nil, err
	}
	orderBy := "id"
	if len(sort) > 0 {
		frag, err := runtime.TranslateSort(sort)
		if err != nil {
			return "", nil, err
		}
		if frag != "" {
			orderBy = frag
		}
	}
	q := "SELECT id, data FROM app_items WHERE " + where + " ORDER BY " + orderBy
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	if skip > 0 {
		q += fmt.Sprintf(" OFFSET %d", skip)
	}
	return q, params, nil
}

// buildUpdateOneQuery stitches together the final UPDATE ... WHERE id = (SELECT
// ... LIMIT 1) statement with its param list. The placeholder layout is:
//
//	$1..$U           — update SET params (translator output)
//	$U+1             — script_run_id
//	$U+2             — caller_user_id
//	$U+3             — tenant_id
//	$U+4             — builder_app_id
//	$U+5             — collection
//	$U+6..           — filter params (translator output)
//
// Using the subquery form keeps us to a single statement so the row pick +
// audit update + mutation all happen under one MVCC snapshot.
func buildUpdateOneQuery(scope Scope, filter map[string]any, setClause string, updateParams []any) (string, []any, error) {
	auditStart := len(updateParams) + 1
	scriptRunPH := auditStart
	callerUserPH := auditStart + 1
	tenantPH := auditStart + 2
	appPH := auditStart + 3
	collPH := auditStart + 4

	params := make([]any, 0, len(updateParams)+5)
	params = append(params, updateParams...)
	params = append(params,
		scope.ScriptRunID, scope.CallerUserID,
		scope.TenantID, scope.BuilderAppID, scope.Collection,
	)

	filterFrag, filterParams, err := runtime.TranslateFilter(filter, auditStart+5)
	if err != nil {
		return "", nil, err
	}
	params = append(params, filterParams...)

	where := fmt.Sprintf("tenant_id = $%d AND builder_app_id = $%d AND collection = $%d",
		tenantPH, appPH, collPH)
	if filterFrag != "" {
		where += " AND " + filterFrag
	}

	q := fmt.Sprintf(`
		UPDATE app_items
		SET %s,
		    script_run_id = $%d,
		    caller_user_id = $%d
		WHERE id = (
		    SELECT id FROM app_items
		    WHERE %s
		    ORDER BY id
		    LIMIT 1
		)
	`, setClause, scriptRunPH, callerUserPH, where)
	return q, params, nil
}

// buildScopedWhere is the single place the tenant/app/collection scoping
// predicate is assembled for read paths (Find, CountDocuments). It composes
// with the mongo translator's filter fragment. Every read path goes through
// here so the scope triple cannot be accidentally dropped.
//
// Returned placeholders start at $1 (tenant_id), $2 (builder_app_id),
// $3 (collection) and the filter translator picks up at $4.
func buildScopedWhere(scope Scope, filter map[string]any) (string, []any, error) {
	where := "tenant_id = $1 AND builder_app_id = $2 AND collection = $3"
	params := []any{scope.TenantID, scope.BuilderAppID, scope.Collection}

	frag, filterParams, err := runtime.TranslateFilter(filter, 4)
	if err != nil {
		return "", nil, err
	}
	if frag != "" {
		where += " AND " + frag
	}
	params = append(params, filterParams...)
	return where, params, nil
}

// pickOneID finds the first row id under the given scope + filter. Used by
// DeleteOne to resolve its target under the same txn. Returns (uuid.Nil, false)
// if no row matches.
func pickOneID(ctx context.Context, tx pgx.Tx, scope Scope, filter map[string]any) (uuid.UUID, bool, error) {
	where, params, err := buildScopedWhere(scope, filter)
	if err != nil {
		return uuid.Nil, false, err
	}
	q := "SELECT id FROM app_items WHERE " + where + " ORDER BY id LIMIT 1"
	var id uuid.UUID
	if err := tx.QueryRow(ctx, q, params...).Scan(&id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, nil
		}
		return uuid.Nil, false, fmt.Errorf("pickOneID: %w", err)
	}
	return id, true, nil
}

// unpackDoc decodes a row's data JSONB into a map and reconciles the _id
// field with the row's id column. If data._id is absent (legacy rows), we
// fill it from the id column so callers always see an _id.
func unpackDoc(id uuid.UUID, raw []byte) (map[string]any, error) {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("unpack data: %w", err)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	if _, ok := doc[sysFieldID]; !ok {
		doc[sysFieldID] = id.String()
	}
	return doc, nil
}

// resolveInsertID honors a caller-supplied _id (parsed as UUID; non-UUID
// strings are rejected — the id column is UUID-typed) or mints a new one.
// Returning a typed UUID keeps the Exec bind simple.
func resolveInsertID(v any) (uuid.UUID, error) {
	if v == nil {
		return uuid.New(), nil
	}
	switch s := v.(type) {
	case string:
		if s == "" {
			return uuid.New(), nil
		}
		id, err := uuid.Parse(s)
		if err != nil {
			return uuid.Nil, fmt.Errorf("_id %q must be a UUID string: %w", s, err)
		}
		return id, nil
	case uuid.UUID:
		if s == uuid.Nil {
			return uuid.New(), nil
		}
		return s, nil
	default:
		return uuid.Nil, fmt.Errorf("_id must be a UUID string, got %T", v)
	}
}
