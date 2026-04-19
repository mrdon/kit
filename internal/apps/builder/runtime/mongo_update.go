// Package runtime: this file translates MongoDB-shaped update documents into a
// single atomic Postgres SET clause against app_items.data (jsonb).
//
// The point of supporting these operators (as opposed to a read-modify-write
// $set-only flow) is atomicity: two concurrent $push calls against the same
// document must both land. Every operator here compiles down to a single
// jsonb expression that the database evaluates against one consistent row
// snapshot, so the writes compose inside one UPDATE statement.
package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Supported top-level MongoDB update operators (v0.1).
const (
	opSet      = "$set"
	opUnset    = "$unset"
	opPush     = "$push"
	opPull     = "$pull"
	opAddToSet = "$addToSet"
	opInc      = "$inc"
)

// TranslateUpdate produces a Postgres SET clause fragment that combines
// atomic jsonb operations. The caller writes:
//
//	UPDATE app_items SET <fragment> WHERE tenant_id=$1 AND ... AND id=$K
//
// startIdx is the first param index the translator can use (caller reserves
// 1..startIdx-1 for its WHERE args). The returned fragment always assigns to
// both `data` (the jsonb payload, with `_updated_at` merged in last so it
// wins) and `updated_at` (the row infra column).
//
// Composition note: operators are applied in a deterministic order — $set
// first (so later operators can see its output via the working expression),
// then $unset, $inc, $push, $addToSet, $pull — and within each operator,
// keys are processed in alphabetical order. This keeps generated SQL stable
// for tests and log diffs without changing semantics (all expressions
// evaluate against the same row snapshot).
func TranslateUpdate(update map[string]any, startIdx int) (string, []any, error) {
	if len(update) == 0 {
		return "", nil, fmt.Errorf("translating update: %w",
			errors.New("update operation must include at least one operator"))
	}

	b := &updateBuilder{
		expr:    "data",
		nextIdx: startIdx,
		params:  []any{},
	}

	// Deterministic operator order. $set runs first so later operators see
	// merged fields through the working expression; $unset follows so removed
	// keys are gone before $inc/$push touch them. Within each operator,
	// keys are sorted alphabetically so SQL is reproducible.
	ordered := []struct {
		name  string
		apply func(*updateBuilder, map[string]any) error
	}{
		{opSet, applySet},
		{opUnset, applyUnset},
		{opInc, applyInc},
		{opPush, applyPush},
		{opAddToSet, applyAddToSet},
		{opPull, applyPull},
	}

	// Reject unknown operators before applying anything.
	known := map[string]struct{}{
		opSet: {}, opUnset: {}, opInc: {}, opPush: {}, opAddToSet: {}, opPull: {},
	}
	for k := range update {
		if _, ok := known[k]; !ok {
			return "", nil, fmt.Errorf("translating update: %w",
				fmt.Errorf("unknown update operator %q", k))
		}
	}

	for _, op := range ordered {
		raw, ok := update[op.name]
		if !ok {
			continue
		}
		args, err := asStringMap(op.name, raw)
		if err != nil {
			return "", nil, fmt.Errorf("translating update: %w", err)
		}
		if err := op.apply(b, args); err != nil {
			return "", nil, fmt.Errorf("translating update: %w", err)
		}
	}

	// Always-appended _updated_at: merge as the final `||` so it wins over
	// any user-supplied _updated_at from $set.
	b.expr = b.expr + " || jsonb_build_object('_updated_at', now()::text)"

	setClause := fmt.Sprintf("data = %s, updated_at = now()", b.expr)
	return setClause, b.params, nil
}

// updateBuilder accumulates the working SQL expression for `data` along with
// the ordered param slice. Every operator helper wraps b.expr and may append
// to b.params, bumping b.nextIdx as it goes.
type updateBuilder struct {
	expr    string
	nextIdx int
	params  []any
}

// addParam reserves the next $N placeholder and appends the Go value to
// params. Returns the placeholder string (e.g. "$3").
func (b *updateBuilder) addParam(v any) string {
	ph := fmt.Sprintf("$%d", b.nextIdx)
	b.nextIdx++
	b.params = append(b.params, v)
	return ph
}

// asStringMap coerces an operator's argument to map[string]any or returns a
// typed error naming the operator.
func asStringMap(op string, raw any) (map[string]any, error) {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s value must be an object, got %T", op, raw)
	}
	if len(m) == 0 {
		return nil, fmt.Errorf("%s must have at least one key", op)
	}
	return m, nil
}

// sortedKeys returns m's keys in alphabetical order.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// applySet: data = data || $N::jsonb for the whole patch object. Cheaper
// than per-key jsonb_set and still atomic because it's one expression.
func applySet(b *updateBuilder, args map[string]any) error {
	// Build a fresh object in sorted key order so the JSON blob is stable.
	patch := map[string]any{}
	for _, k := range sortedKeys(args) {
		patch[k] = args[k]
	}
	blob, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshalling $set patch: %w", err)
	}
	ph := b.addParam(string(blob))
	b.expr = fmt.Sprintf("(%s || %s::jsonb)", b.expr, ph)
	return nil
}

// applyUnset: data = data - $N for each key. Chained subtraction is atomic
// against the row snapshot. Values in the unset object are ignored (Mongo
// convention).
func applyUnset(b *updateBuilder, args map[string]any) error {
	for _, k := range sortedKeys(args) {
		ph := b.addParam(k)
		b.expr = fmt.Sprintf("(%s - %s)", b.expr, ph)
	}
	return nil
}

// applyInc: for each key, wrap expr with jsonb_set that adds N to the
// current numeric value (treating missing/null as 0). Values in args must
// be numeric (int, int64, float64, json.Number).
func applyInc(b *updateBuilder, args map[string]any) error {
	for _, k := range sortedKeys(args) {
		num, err := toNumericParam(args[k])
		if err != nil {
			return fmt.Errorf("$inc key %q: %w", k, err)
		}
		ph := b.addParam(num)
		path := jsonbPath(k)
		b.expr = fmt.Sprintf(
			"jsonb_set(%s, %s, to_jsonb(COALESCE((data->>%s)::numeric, 0) + %s))",
			b.expr, path, sqlLiteral(k), ph,
		)
	}
	return nil
}

// applyPush: for each key, append value to the array at that key. Uses the
// ORIGINAL `data` for reading the current array (COALESCE(data->'key', '[]'))
// so concurrent writers see a consistent snapshot; the result is written
// into the working expression via jsonb_set.
func applyPush(b *updateBuilder, args map[string]any) error {
	for _, k := range sortedKeys(args) {
		blob, err := json.Marshal([]any{args[k]})
		if err != nil {
			return fmt.Errorf("marshalling $push value for %q: %w", k, err)
		}
		ph := b.addParam(string(blob))
		path := jsonbPath(k)
		b.expr = fmt.Sprintf(
			"jsonb_set(%s, %s, COALESCE(data->%s, '[]'::jsonb) || %s::jsonb)",
			b.expr, path, sqlLiteral(k), ph,
		)
	}
	return nil
}

// applyAddToSet: conditional $push — no-op if the value is already present.
// Uses @> containment against the original data to decide.
func applyAddToSet(b *updateBuilder, args map[string]any) error {
	for _, k := range sortedKeys(args) {
		// Containment check wants the bare value, not wrapped in a list —
		// jsonb '@>' checks whether the right side is contained in the left,
		// and for arrays that means "does any element equal this value".
		// So we pass the value itself as a single-element array literal on
		// both sides: data->'key' @> [value] means "array contains value".
		blob, err := json.Marshal([]any{args[k]})
		if err != nil {
			return fmt.Errorf("marshalling $addToSet value for %q: %w", k, err)
		}
		ph := b.addParam(string(blob))
		path := jsonbPath(k)
		lit := sqlLiteral(k)
		b.expr = fmt.Sprintf(
			"jsonb_set(%s, %s, CASE WHEN COALESCE(data->%s, '[]'::jsonb) @> %s::jsonb "+
				"THEN COALESCE(data->%s, '[]'::jsonb) "+
				"ELSE COALESCE(data->%s, '[]'::jsonb) || %s::jsonb END)",
			b.expr, path, lit, ph, lit, lit, ph,
		)
	}
	return nil
}

// applyPull: remove matching elements from the array at key.
func applyPull(b *updateBuilder, args map[string]any) error {
	for _, k := range sortedKeys(args) {
		blob, err := json.Marshal(args[k])
		if err != nil {
			return fmt.Errorf("marshalling $pull value for %q: %w", k, err)
		}
		ph := b.addParam(string(blob))
		path := jsonbPath(k)
		lit := sqlLiteral(k)
		// Rebuild the array filtering out elements equal to the pulled value.
		// Reads from original `data` (same-row snapshot), writes through the
		// working expression via jsonb_set.
		b.expr = fmt.Sprintf(
			"jsonb_set(%s, %s, (SELECT COALESCE(jsonb_agg(x), '[]'::jsonb) "+
				"FROM jsonb_array_elements(COALESCE(data->%s, '[]'::jsonb)) x "+
				"WHERE x <> %s::jsonb))",
			b.expr, path, lit, ph,
		)
	}
	return nil
}

// jsonbPath renders a single-key jsonb path literal like '{key}' for
// jsonb_set. We escape embedded single quotes so admin-provided keys can't
// break out of the literal. Backslashes inside jsonb path literals are
// treated as escape characters, so they're doubled too.
func jsonbPath(key string) string {
	esc := strings.ReplaceAll(key, `\`, `\\`)
	esc = strings.ReplaceAll(esc, `'`, `''`)
	// jsonb_set path element: '{"key"}' — wrapping in double quotes handles
	// keys with commas, spaces, or other special chars inside the path text.
	inner := strings.ReplaceAll(esc, `"`, `\"`)
	return fmt.Sprintf(`'{"%s"}'`, inner)
}

// sqlLiteral wraps a string as a single-quoted SQL literal, escaping any
// embedded quotes. Used for jsonb key lookups like data->'key'. Backslashes
// are not special in standard-conforming SQL string literals, so we only
// escape the quote.
func sqlLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, `'`, `''`) + "'"
}

// toNumericParam normalizes a Go value destined for $inc into a type pgx
// will bind as numeric. Mongo-shaped inputs come in as int, int64, float64,
// or json.Number depending on decoder; all are coerced to float64 for
// simplicity. Non-numeric values (including bool) error out.
func toNumericParam(v any) (any, error) {
	switch n := v.(type) {
	case int:
		return float64(n), nil
	case int32:
		return float64(n), nil
	case int64:
		return float64(n), nil
	case float32:
		return float64(n), nil
	case float64:
		return n, nil
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return nil, fmt.Errorf("value %q is not numeric", n)
		}
		return f, nil
	default:
		return nil, fmt.Errorf("value %v (%T) is not numeric", v, v)
	}
}
