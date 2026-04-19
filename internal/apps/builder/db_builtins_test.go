// Package builder: db_builtins_test.go exercises the Monty→ItemService
// bridge end-to-end. Every test compiles a Python script, runs it under the
// shared MontyEngine, and asserts the resulting rows in Postgres.
//
// The shared runner is built once in TestMain (wazero bring-up is ~5s);
// each test still gets its own tenant+app fixture so parallel runs don't
// clash.
package builder

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/builder/runtime"
)

// testRunner is a package-scoped Monty Runner shared across every test in
// this package. It's expensive to build (~5s) so amortise the cost.
var (
	testRunner *runtime.Runner
	testEngine *runtime.MontyEngine
)

// TestMain stands up the runner/engine once per `go test` invocation.
func TestMain(m *testing.M) {
	r, err := runtime.New()
	if err != nil {
		fmt.Fprintf(os.Stderr, "db_builtins test: failed to create runner: %v\n", err)
		os.Exit(1)
	}
	testRunner = r
	testEngine = runtime.NewMontyEngine(r)
	code := m.Run()
	_ = r.Close()
	os.Exit(code)
}

// newCtx returns a generous per-test context. Monty boots fast but the
// Postgres RTT inside the dispatcher can add up across chained calls.
func newCtx(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 30*time.Second)
}

// runScript compiles+executes a Python `main()` function with the supplied
// DBBuiltins wired up. Using a single convention keeps each test's Python
// focused on the actual roundtrip under test.
func runScript(t *testing.T, ctx context.Context, src string, bundle *DBBuiltins) (any, runtime.Metadata, error) {
	t.Helper()
	mod, err := testEngine.Compile(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	caps := &runtime.Capabilities{
		BuiltIns:      bundle.BuiltIns,
		BuiltInParams: bundle.Params,
		RunID:         uuid.New(),
	}
	return testEngine.Run(ctx, mod, "main", nil, caps)
}

// TestDBBuiltins_HappyPath: insert, find_one, find, update, count in a
// single script. Ends by returning the count so we can assert the end-to-
// end pipeline worked.
func TestDBBuiltins_HappyPath(t *testing.T) {
	f := newItemFixture(t)
	ctx, cancel := newCtx(t)
	defer cancel()

	runID := uuid.New()
	bundle := BuildDBBuiltins(f.svc, f.tenant.ID, f.appID, f.userID, &runID, 0)

	src := `
def main():
    user = db_insert_one("contacts", {"name": "Jane", "tier": "silver"})
    by_id = db_find_one("contacts", {"_id": user["_id"]})
    if by_id is None or by_id["name"] != "Jane":
        raise Exception("find_one round-trip failed: " + str(by_id))
    rows = db_find("contacts", {}, limit=10, sort=[("_created_at", -1)])
    if len(rows) != 1:
        raise Exception("find returned wrong count: " + str(len(rows)))
    db_update_one("contacts", {"_id": user["_id"]}, {"$set": {"tier": "gold"}})
    return db_count_documents("contacts", {"tier": "gold"})
`

	result, meta, err := runScript(t, ctx, src, bundle)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// count_documents returns int64 on Go side; it crosses into Monty as
	// a JSON number, then back to Go as float64.
	got, ok := result.(float64)
	if !ok {
		t.Fatalf("result type = %T, want float64", result)
	}
	if got != 1 {
		t.Fatalf("count = %v, want 1", got)
	}
	if meta.ExternalCalls != 5 {
		t.Fatalf("ExternalCalls = %d, want 5", meta.ExternalCalls)
	}

	// Verify the row actually landed in Postgres with the updated tier.
	doc, err := f.svc.FindOne(ctx, f.scope("contacts"), map[string]any{"name": "Jane"})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if doc == nil {
		t.Fatal("row missing after script")
	}
	if doc["tier"] != "gold" {
		t.Fatalf("tier = %v, want gold", doc["tier"])
	}
}

// TestDBBuiltins_AtomicPushFromScript: a script $pushes twice into an
// array; both land atomically via TranslateUpdate.
func TestDBBuiltins_AtomicPushFromScript(t *testing.T) {
	f := newItemFixture(t)
	ctx, cancel := newCtx(t)
	defer cancel()

	bundle := BuildDBBuiltins(f.svc, f.tenant.ID, f.appID, f.userID, nil, 0)

	src := `
def main():
    doc = db_insert_one("lists", {"name": "L", "items": []})
    db_update_one("lists", {"_id": doc["_id"]}, {"$push": {"items": "one"}})
    db_update_one("lists", {"_id": doc["_id"]}, {"$push": {"items": "two"}})
    out = db_find_one("lists", {"_id": doc["_id"]})
    return out["items"]
`

	result, _, err := runScript(t, ctx, src, bundle)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	items, ok := result.([]any)
	if !ok {
		t.Fatalf("result type = %T, want []any", result)
	}
	if len(items) != 2 || items[0] != "one" || items[1] != "two" {
		t.Fatalf("items = %v, want [one two]", items)
	}

	// Cross-check on the Go side: verify the row in Postgres shows the
	// same two items (not just what the script saw).
	doc, err := f.svc.FindOne(ctx, f.scope("lists"), map[string]any{"name": "L"})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	dbItems, ok := doc["items"].([]any)
	if !ok || len(dbItems) != 2 {
		t.Fatalf("db items = %v", doc["items"])
	}
}

// TestDBBuiltins_QuotaEnforcement: a 3-call budget; 4th call errors.
func TestDBBuiltins_QuotaEnforcement(t *testing.T) {
	f := newItemFixture(t)
	ctx, cancel := newCtx(t)
	defer cancel()

	bundle := BuildDBBuiltins(f.svc, f.tenant.ID, f.appID, f.userID, nil, 3)

	src := `
def main():
    db_insert_one("q", {"n": 1})
    db_insert_one("q", {"n": 2})
    db_insert_one("q", {"n": 3})
    db_insert_one("q", {"n": 4})
    return "unreached"
`

	_, _, err := runScript(t, ctx, src, bundle)
	if err == nil {
		t.Fatal("expected quota error, got nil")
	}
	if !strings.Contains(err.Error(), "db quota exhausted") {
		t.Fatalf("error does not mention quota: %v", err)
	}
	if !strings.Contains(err.Error(), "3") {
		t.Fatalf("error should mention cap of 3: %v", err)
	}

	// Exactly three rows should have landed before the 4th call errored.
	n, err := f.svc.CountDocuments(ctx, f.scope("q"), nil)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 3 {
		t.Fatalf("rows inserted = %d, want 3", n)
	}

	// CallsRemaining should be zero after exhaustion.
	if got := bundle.CallsRemaining(); got != 0 {
		t.Fatalf("CallsRemaining = %d, want 0", got)
	}
}

// TestDBBuiltins_TenantAppIsolation: two separate scope triples must not
// see each other's rows via the bridge.
func TestDBBuiltins_TenantAppIsolation(t *testing.T) {
	fA := newItemFixture(t)
	fB := newItemFixture(t)
	ctx, cancel := newCtx(t)
	defer cancel()

	bundleA := BuildDBBuiltins(fA.svc, fA.tenant.ID, fA.appID, fA.userID, nil, 0)
	bundleB := BuildDBBuiltins(fB.svc, fB.tenant.ID, fB.appID, fB.userID, nil, 0)

	// Script A inserts a row.
	insertSrc := `
def main():
    db_insert_one("shared", {"tag": "A"})
    return db_count_documents("shared", {})
`
	countA, _, err := runScript(t, ctx, insertSrc, bundleA)
	if err != nil {
		t.Fatalf("insert in A: %v", err)
	}
	if countA.(float64) != 1 {
		t.Fatalf("count in A = %v, want 1", countA)
	}

	// Script B (different tenant+app) queries and must see zero.
	bSrc := `
def main():
    rows = db_find("shared", {})
    return len(rows)
`
	lenB, _, err := runScript(t, ctx, bSrc, bundleB)
	if err != nil {
		t.Fatalf("find in B: %v", err)
	}
	if lenB.(float64) != 0 {
		t.Fatalf("tenant B saw %v rows under isolation", lenB)
	}

	// Insert a row in B and confirm A still only sees its own.
	if _, _, err := runScript(t, ctx, insertSrc, bundleB); err != nil {
		t.Fatalf("insert in B: %v", err)
	}
	rowsA, _, err := runScript(t, ctx, `
def main():
    rows = db_find("shared", {})
    return [r["tag"] for r in rows]
`, bundleA)
	if err != nil {
		t.Fatalf("find in A post-B: %v", err)
	}
	list := rowsA.([]any)
	if len(list) != 1 || list[0] != "A" {
		t.Fatalf("tenant A saw %v after B inserted, want just [A]", list)
	}
}

// TestDBBuiltins_FilterErrorPropagates: a script passes a filter with an
// unknown operator; the error surfaces from the bridge and halts the run.
func TestDBBuiltins_FilterErrorPropagates(t *testing.T) {
	f := newItemFixture(t)
	ctx, cancel := newCtx(t)
	defer cancel()

	bundle := BuildDBBuiltins(f.svc, f.tenant.ID, f.appID, f.userID, nil, 0)

	src := `
def main():
    # $bogus is not a supported operator; TranslateFilter should reject it.
    return db_find("x", {"name": {"$bogus": 1}})
`
	_, _, err := runScript(t, ctx, src, bundle)
	if err == nil {
		t.Fatal("expected error for invalid operator, got nil")
	}
	// Host errors come back wrapped (not as *MontyError, per the current
	// wasm bridge). We only care that the translator's message is carried
	// through so admins can debug.
	if !strings.Contains(err.Error(), "db_find") {
		t.Fatalf("error should mention db_find, got: %v", err)
	}
	// The translator error mentions "unknown operator".
	if !strings.Contains(err.Error(), "unknown operator") &&
		!strings.Contains(err.Error(), "$bogus") {
		t.Fatalf("error should mention the bad operator, got: %v", err)
	}

	// Sanity: errors.Is/As on known sentinels shouldn't panic with this
	// wrapped error — it's a regular wrapped Go error.
	var montyErr *runtime.MontyError
	_ = errors.As(err, &montyErr)
}

// TestDBBuiltins_MissingCollectionErrors: calling db_insert_one without a
// collection string must be rejected by the bridge before touching the DB.
func TestDBBuiltins_MissingCollectionErrors(t *testing.T) {
	f := newItemFixture(t)
	ctx, cancel := newCtx(t)
	defer cancel()

	bundle := BuildDBBuiltins(f.svc, f.tenant.ID, f.appID, f.userID, nil, 0)

	src := `
def main():
    return db_insert_one(None, {"x": 1})
`
	_, _, err := runScript(t, ctx, src, bundle)
	if err == nil {
		t.Fatal("expected error for missing collection, got nil")
	}
	if !strings.Contains(err.Error(), "collection") {
		t.Fatalf("error should mention collection, got: %v", err)
	}
}

// TestDBBuiltins_UnlimitedQuota: maxCalls=0 means no quota; make 10 calls.
func TestDBBuiltins_UnlimitedQuota(t *testing.T) {
	f := newItemFixture(t)
	ctx, cancel := newCtx(t)
	defer cancel()

	bundle := BuildDBBuiltins(f.svc, f.tenant.ID, f.appID, f.userID, nil, 0)

	src := `
def main():
    for i in range(10):
        db_insert_one("u", {"i": i})
    return db_count_documents("u", {})
`
	result, _, err := runScript(t, ctx, src, bundle)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.(float64) != 10 {
		t.Fatalf("count = %v, want 10", result)
	}
	if got := bundle.CallsRemaining(); got != -1 {
		t.Fatalf("CallsRemaining with unlimited = %d, want -1", got)
	}
}
