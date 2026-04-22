package runtime

import (
	"fmt"
	"sort"
	"strings"
)

// TranslateFilter produces a SQL predicate fragment that applies only to the
// data JSONB column of the app_items table. The caller is responsible for
// prepending tenant/app/collection scoping predicates (tenant_id = $1 AND
// builder_app_id = $2 AND collection = $3) — this function only translates
// the MongoDB-style filter dict supplied by admin scripts.
//
// startIdx is the number to start $N placeholders at (usually 4, since $1/$2/$3
// are tenant/app/collection on the caller side). The returned SQL fragment
// contains no leading "WHERE" keyword; callers splice it into a larger query
// with " AND " between the scoping predicate and this fragment.
//
// An empty or nil filter returns ("", []any{}, nil) — the caller should detect
// the empty string and omit the extra AND entirely.
//
// Supported operators (v0.1):
//   - Implicit equality: {"name": "Jane"}                → data->>'name' = $N
//   - $eq:               {"name": {"$eq": "Jane"}}       → data->>'name' = $N
//   - $ne:                                               → data->>'name' != $N
//   - $gt/$gte/$lt/$lte: numeric comparisons on          → (data->>'key')::numeric <op> $N
//   - $in:                                               → data->>'key' = ANY($N::text[])
//
// Special key handling:
//   - "_id" maps to the id column directly, not data->>'_id'.
//
// Booleans are compared via (data->>'key')::boolean for equality operators so
// scripts can write {"active": true}.
//
// Top-level dict entries are joined by AND. $or / $and are not supported in
// v0.1 and will surface as "unknown operator" errors if encountered at the
// top level.
func TranslateFilter(filter map[string]any, startIdx int) (string, []any, error) {
	if len(filter) == 0 {
		return "", []any{}, nil
	}

	// Sort keys so the generated SQL is deterministic (tests can assert exact
	// strings; callers get stable query plans in pg_stat_statements).
	keys := make([]string, 0, len(filter))
	for k := range filter {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	clauses := make([]string, 0, len(filter))
	params := make([]any, 0, len(filter))
	idx := startIdx

	for _, key := range keys {
		val := filter[key]
		clause, used, err := translateKey(key, val, &idx, &params)
		if err != nil {
			return "", nil, fmt.Errorf("translating filter key %q: %w", key, err)
		}
		_ = used
		clauses = append(clauses, clause)
	}

	return strings.Join(clauses, " AND "), params, nil
}

// translateKey renders a single top-level (key, value) pair. It appends any
// generated parameters onto *params and advances *idx. The boolean returned is
// reserved for callers that want to know whether a placeholder was consumed
// (currently unused by TranslateFilter but kept for symmetry).
func translateKey(key string, val any, idx *int, params *[]any) (string, bool, error) {
	// Operator dict: {"$gt": 10, "$lt": 20} becomes a range predicate against
	// the same field (joined with AND).
	if opMap, ok := val.(map[string]any); ok {
		return translateOpMap(key, opMap, idx, params)
	}
	// Bare value — implicit equality.
	return translateEquality(key, val, idx, params)
}

// translateEquality renders "key = $N" (or the _id / column variant). Booleans
// are cast so JSON-native values compare correctly.
func translateEquality(key string, val any, idx *int, params *[]any) (string, bool, error) {
	if key == "_id" {
		sql := fmt.Sprintf("id = $%d", *idx)
		*params = append(*params, val)
		*idx++
		return sql, true, nil
	}
	if _, isBool := val.(bool); isBool {
		sql := fmt.Sprintf("(data->>'%s')::boolean = $%d", escapeKey(key), *idx)
		*params = append(*params, val)
		*idx++
		return sql, true, nil
	}
	sql := fmt.Sprintf("data->>'%s' = $%d", escapeKey(key), *idx)
	*params = append(*params, val)
	*idx++
	return sql, true, nil
}

// translateOpMap renders an operator dict like {"$gt": 10, "$lt": 20}. Multiple
// operators on the same key are joined with AND.
func translateOpMap(key string, ops map[string]any, idx *int, params *[]any) (string, bool, error) {
	opKeys := make([]string, 0, len(ops))
	for k := range ops {
		opKeys = append(opKeys, k)
	}
	sort.Strings(opKeys)

	parts := make([]string, 0, len(ops))
	for _, op := range opKeys {
		raw := ops[op]
		part, err := renderOp(key, op, raw, idx, params)
		if err != nil {
			return "", false, err
		}
		parts = append(parts, part)
	}
	if len(parts) == 1 {
		return parts[0], true, nil
	}
	return "(" + strings.Join(parts, " AND ") + ")", true, nil
}

// renderOp handles a single operator against a single field.
func renderOp(key, op string, val any, idx *int, params *[]any) (string, error) {
	switch op {
	case "$eq":
		sql, _, err := translateEquality(key, val, idx, params)
		return sql, err
	case "$ne":
		if key == "_id" {
			sql := fmt.Sprintf("id != $%d", *idx)
			*params = append(*params, val)
			*idx++
			return sql, nil
		}
		if _, isBool := val.(bool); isBool {
			sql := fmt.Sprintf("(data->>'%s')::boolean != $%d", escapeKey(key), *idx)
			*params = append(*params, val)
			*idx++
			return sql, nil
		}
		sql := fmt.Sprintf("data->>'%s' != $%d", escapeKey(key), *idx)
		*params = append(*params, val)
		*idx++
		return sql, nil
	case "$gt", "$gte", "$lt", "$lte":
		sqlOp := map[string]string{
			"$gt":  ">",
			"$gte": ">=",
			"$lt":  "<",
			"$lte": "<=",
		}[op]
		// Numeric comparison when the script passed a number; otherwise
		// fall back to lexical string comparison on the jsonb text form.
		// Lexical ordering is what admins actually want for RFC3339
		// timestamps, YYYY-MM-DD dates, and sortable ID strings — the
		// whole "strings sort lexically, you get ORDER BY almost for
		// free" convention we push in the admin guide depends on range
		// predicates working on strings too.
		if isNumeric(val) {
			sql := fmt.Sprintf("(data->>'%s')::numeric %s $%d", escapeKey(key), sqlOp, *idx)
			*params = append(*params, val)
			*idx++
			return sql, nil
		}
		if _, ok := val.(string); ok {
			sql := fmt.Sprintf("data->>'%s' %s $%d", escapeKey(key), sqlOp, *idx)
			*params = append(*params, val)
			*idx++
			return sql, nil
		}
		return "", fmt.Errorf("operator %s requires numeric or string value, got %T", op, val)
	case "$in":
		list, ok := toList(val)
		if !ok {
			return "", fmt.Errorf("operator $in requires a list, got %T", val)
		}
		if len(list) == 0 {
			// Empty $in is a contradiction — nothing can match.
			return "FALSE", nil
		}
		strs := make([]string, len(list))
		for i, item := range list {
			strs[i] = fmt.Sprintf("%v", item)
		}
		if key == "_id" {
			sql := fmt.Sprintf("id::text = ANY($%d::text[])", *idx)
			*params = append(*params, strs)
			*idx++
			return sql, nil
		}
		sql := fmt.Sprintf("data->>'%s' = ANY($%d::text[])", escapeKey(key), *idx)
		*params = append(*params, strs)
		*idx++
		return sql, nil
	default:
		return "", fmt.Errorf("unknown operator %q", op)
	}
}

// TranslateSort converts a Mongo-style sort spec into an ORDER BY fragment.
// The input is expected to be a list of 2-tuples: [(field, 1|-1), ...] where
// 1 is ASC and -1 is DESC. Each tuple is represented as []any{string, number}
// because that's what Python tuples look like after coming through monty.
//
// Returns "" for nil or empty sort, e.g. caller can just omit ORDER BY.
// Otherwise returns the "(data->>'foo') ASC, (data->>'bar') DESC" fragment
// (no leading "ORDER BY" keyword).
func TranslateSort(sortSpec []any) (string, error) {
	if len(sortSpec) == 0 {
		return "", nil
	}
	parts := make([]string, 0, len(sortSpec))
	for i, entry := range sortSpec {
		tuple, ok := toList(entry)
		if !ok || len(tuple) != 2 {
			return "", fmt.Errorf("sort entry %d: expected (field, direction) tuple, got %T", i, entry)
		}
		field, ok := tuple[0].(string)
		if !ok {
			return "", fmt.Errorf("sort entry %d: field must be string, got %T", i, tuple[0])
		}
		dir, err := sortDirection(tuple[1])
		if err != nil {
			return "", fmt.Errorf("sort entry %d: %w", i, err)
		}
		if field == "_id" {
			parts = append(parts, "id "+dir)
		} else {
			parts = append(parts, fmt.Sprintf("(data->>'%s') %s", escapeKey(field), dir))
		}
	}
	return strings.Join(parts, ", "), nil
}

// sortDirection normalizes 1/-1 (in any numeric form) to ASC/DESC.
func sortDirection(v any) (string, error) {
	switch n := v.(type) {
	case int:
		return dirFromInt(int64(n))
	case int32:
		return dirFromInt(int64(n))
	case int64:
		return dirFromInt(n)
	case float32:
		return dirFromInt(int64(n))
	case float64:
		return dirFromInt(int64(n))
	default:
		return "", fmt.Errorf("direction must be 1 or -1, got %T", v)
	}
}

func dirFromInt(n int64) (string, error) {
	switch n {
	case 1:
		return "ASC", nil
	case -1:
		return "DESC", nil
	default:
		return "", fmt.Errorf("direction must be 1 or -1, got %d", n)
	}
}

// isNumeric reports whether v is one of the Go numeric types a script might
// pass through — anything json/monty would hand us for a number.
func isNumeric(v any) bool {
	switch v.(type) {
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return true
	default:
		return false
	}
}

// toList coerces []any or []string or []interface{} into a []any slice. Other
// slice types (e.g. []int) are also handled via a best-effort conversion so
// scripts passing a pure-int list to $in still work.
func toList(v any) ([]any, bool) {
	switch lst := v.(type) {
	case []any:
		return lst, true
	case []string:
		out := make([]any, len(lst))
		for i, s := range lst {
			out[i] = s
		}
		return out, true
	case []int:
		out := make([]any, len(lst))
		for i, n := range lst {
			out[i] = n
		}
		return out, true
	case []float64:
		out := make([]any, len(lst))
		for i, n := range lst {
			out[i] = n
		}
		return out, true
	default:
		return nil, false
	}
}

// escapeKey defends against apostrophes in field names. JSONB field names are
// user-controlled (via admin scripts), so we rewrite any single quote as the
// SQL-safe doubled form. We do NOT parameterize the field name itself because
// Postgres doesn't bind identifiers — data->>$N is not a thing.
func escapeKey(k string) string {
	return strings.ReplaceAll(k, "'", "''")
}
