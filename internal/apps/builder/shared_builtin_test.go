// Package builder: shared_builtin_test.go drives the `shared(...)` host
// function end-to-end — admin inserts a helper script into the scripts
// table, another script calls it via `shared("helper", "fn", **kw)`, and we
// assert the dispatch works, cross-app is rejected, and nested shared()
// chains thread the parent's capabilities through.
package builder

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/builder/runtime"
)

// insertHelperScript inserts a scripts + script_revisions row and points
// current_rev_id at the revision. The body is raw Python source; tests
// provide it inline. Mirrors the INSERT in createScriptRun but skips the
// script_runs row — we're exercising shared(), not an external runner.
func insertHelperScript(t *testing.T, f *itemFixture, appID uuid.UUID, name, body string) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	var scriptID uuid.UUID
	err := f.pool.QueryRow(ctx, `
		INSERT INTO scripts (tenant_id, builder_app_id, name, description, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, f.tenant.ID, appID, name, "shared-builtin test", f.userID).Scan(&scriptID)
	if err != nil {
		t.Fatalf("insert script %q: %v", name, err)
	}

	var revID uuid.UUID
	err = f.pool.QueryRow(ctx, `
		INSERT INTO script_revisions (script_id, body, created_by)
		VALUES ($1, $2, $3)
		RETURNING id
	`, scriptID, body, f.userID).Scan(&revID)
	if err != nil {
		t.Fatalf("insert script_revision: %v", err)
	}

	_, err = f.pool.Exec(ctx, `
		UPDATE scripts SET current_rev_id = $1 WHERE id = $2
	`, revID, scriptID)
	if err != nil {
		t.Fatalf("update current_rev_id: %v", err)
	}
	return scriptID
}

// runWithShared compiles src and runs it under Capabilities that include
// the `shared` builtin wired up against the given builder-app context.
// Threads the parent's caps onto ctx so nested shared() calls find the
// same BuiltIns/Limits.
func runWithShared(
	t *testing.T,
	ctx context.Context,
	src string,
	bundle *SharedBuiltin,
	counter *atomic.Int64,
) (any, runtime.Metadata, error) {
	t.Helper()
	_ = counter // counter is observed by the caller directly

	mod, err := testEngine.Compile(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	caps := &runtime.Capabilities{
		BuiltIns:      bundle.BuiltIns,
		BuiltInParams: bundle.Params,
		RunID:         uuid.New(),
	}
	ctx = ShareEngineCaps(ctx, caps)
	return testEngine.Run(ctx, mod, "main", nil, caps)
}

// TestSharedBuiltin_HappyPath: a helper script "utils" defines
// format_phone(phone); caller script invokes shared("utils", "format_phone",
// phone="8885551234") and asserts the formatted result.
func TestSharedBuiltin_HappyPath(t *testing.T) {
	f := newItemFixture(t)
	ctx, cancel := newCtx(t)
	defer cancel()

	insertHelperScript(t, f, f.appID, "utils", `
def format_phone(phone):
    digits = "".join([c for c in phone if c.isdigit()])
    if len(digits) != 10:
        return phone
    return "(" + digits[:3] + ") " + digits[3:6] + "-" + digits[6:]
`)

	counter := &atomic.Int64{}
	bundle := BuildSharedBuiltin(f.pool, testEngine, f.tenant.ID, f.appID, f.userID, nil, nil, counter)

	src := `
def main():
    return shared("utils", "format_phone", phone="8885551234")
`
	result, _, err := runWithShared(t, ctx, src, bundle, counter)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, want := result.(string), "(888) 555-1234"; got != want {
		t.Fatalf("result = %q, want %q", got, want)
	}
	if got := counter.Load(); got != 1 {
		t.Fatalf("sharedCallsCounter = %d, want 1", got)
	}
}

// TestSharedBuiltin_CrossAppRejected: app A exposes "utils", app B's script
// tries to call it — must error with "not found in app". Proves the
// builder_app_id scoping clause.
func TestSharedBuiltin_CrossAppRejected(t *testing.T) {
	f := newItemFixture(t)
	ctx, cancel := newCtx(t)
	defer cancel()

	// Insert "utils" into a second builder_app (appB) in the same tenant.
	var appB uuid.UUID
	err := f.pool.QueryRow(ctx, `
		INSERT INTO builder_apps (tenant_id, name, description, created_by)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, f.tenant.ID, "test-app-B-"+uuid.NewString()[:8], "second app", f.userID).Scan(&appB)
	if err != nil {
		t.Fatalf("create appB: %v", err)
	}
	insertHelperScript(t, f, appB, "utils", `
def format_phone(phone):
    return "B:" + phone
`)

	// Caller context is pinned to the ORIGINAL app (f.appID).
	counter := &atomic.Int64{}
	bundle := BuildSharedBuiltin(f.pool, testEngine, f.tenant.ID, f.appID, f.userID, nil, nil, counter)

	src := `
def main():
    return shared("utils", "format_phone", phone="8885551234")
`
	_, _, err = runWithShared(t, ctx, src, bundle, counter)
	if err == nil {
		t.Fatal("expected error (cross-app lookup), got nil")
	}
	if !strings.Contains(err.Error(), "not found in app") {
		t.Fatalf("error should mention not found in app, got %v", err)
	}
	if got := counter.Load(); got != 0 {
		t.Fatalf("counter should be 0 on rejection, got %d", got)
	}
}

// TestSharedBuiltin_UnknownScript: calling shared() with a name that simply
// doesn't exist anywhere in this app returns the same friendly error.
func TestSharedBuiltin_UnknownScript(t *testing.T) {
	f := newItemFixture(t)
	ctx, cancel := newCtx(t)
	defer cancel()

	counter := &atomic.Int64{}
	bundle := BuildSharedBuiltin(f.pool, testEngine, f.tenant.ID, f.appID, f.userID, nil, nil, counter)

	src := `
def main():
    return shared("nope", "does_not_matter", x=1)
`
	_, _, err := runWithShared(t, ctx, src, bundle, counter)
	if err == nil {
		t.Fatal("expected error for unknown script")
	}
	if !strings.Contains(err.Error(), `"nope"`) {
		t.Fatalf("error should mention the missing script name, got %v", err)
	}
}

// TestSharedBuiltin_MissingArgs: shared("utils") or shared() must surface a
// clear error about missing arguments.
func TestSharedBuiltin_MissingArgs(t *testing.T) {
	f := newItemFixture(t)
	ctx, cancel := newCtx(t)
	defer cancel()

	counter := &atomic.Int64{}
	bundle := BuildSharedBuiltin(f.pool, testEngine, f.tenant.ID, f.appID, f.userID, nil, nil, counter)

	// Missing fn — Python-side call provides only the script name.
	src := `
def main():
    return shared("utils")
`
	_, _, err := runWithShared(t, ctx, src, bundle, counter)
	if err == nil {
		t.Fatal("expected error for missing fn")
	}
	if !strings.Contains(err.Error(), "fn") {
		t.Fatalf("error should mention missing fn argument, got %v", err)
	}
}

// TestSharedBuiltin_NoCurrentRevision: a script row exists but its
// current_rev_id is NULL (e.g. freshly created, no body committed). Should
// surface a distinct error from "not found".
func TestSharedBuiltin_NoCurrentRevision(t *testing.T) {
	f := newItemFixture(t)
	ctx, cancel := newCtx(t)
	defer cancel()

	// INSERT a scripts row WITHOUT updating current_rev_id.
	_, err := f.pool.Exec(ctx, `
		INSERT INTO scripts (tenant_id, builder_app_id, name, description, created_by)
		VALUES ($1, $2, $3, $4, $5)
	`, f.tenant.ID, f.appID, "blank", "shared test", f.userID)
	if err != nil {
		t.Fatalf("insert script: %v", err)
	}

	counter := &atomic.Int64{}
	bundle := BuildSharedBuiltin(f.pool, testEngine, f.tenant.ID, f.appID, f.userID, nil, nil, counter)

	src := `
def main():
    return shared("blank", "whatever", x=1)
`
	_, _, err = runWithShared(t, ctx, src, bundle, counter)
	if err == nil {
		t.Fatal("expected error for no current revision")
	}
	if !strings.Contains(err.Error(), "no current revision") {
		t.Fatalf("error should mention no current revision, got %v", err)
	}
}

// TestSharedBuiltin_Nested: script A calls shared("B", "fn_b"), B's fn_b
// calls shared("C", "fn_c"). Both hops must fire and the counter reaches 2.
func TestSharedBuiltin_Nested(t *testing.T) {
	f := newItemFixture(t)
	ctx, cancel := newCtx(t)
	defer cancel()

	insertHelperScript(t, f, f.appID, "B", `
def fn_b(seed):
    return shared("C", "fn_c", seed=seed) + "-B"
`)
	insertHelperScript(t, f, f.appID, "C", `
def fn_c(seed):
    return seed + "-C"
`)

	counter := &atomic.Int64{}
	bundle := BuildSharedBuiltin(f.pool, testEngine, f.tenant.ID, f.appID, f.userID, nil, nil, counter)

	src := `
def main():
    return shared("B", "fn_b", seed="root")
`
	result, _, err := runWithShared(t, ctx, src, bundle, counter)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, want := result.(string), "root-C-B"; got != want {
		t.Fatalf("nested result = %q, want %q", got, want)
	}
	if got := counter.Load(); got != 2 {
		t.Fatalf("sharedCallsCounter = %d, want 2 (A->B + B->C)", got)
	}
}

// TestSharedBuiltin_NoScriptRuns: after a shared() call completes, no new
// script_runs rows should exist in the tenant. That's the whole point of
// this primitive — it's the cheap helper path.
func TestSharedBuiltin_NoScriptRuns(t *testing.T) {
	f := newItemFixture(t)
	ctx, cancel := newCtx(t)
	defer cancel()

	insertHelperScript(t, f, f.appID, "utils", `
def echo(v):
    return v
`)

	// Baseline: 0 runs for this tenant to start.
	var before int
	if err := f.pool.QueryRow(ctx, `SELECT COUNT(*) FROM script_runs WHERE tenant_id = $1`, f.tenant.ID).Scan(&before); err != nil {
		t.Fatalf("count runs before: %v", err)
	}

	counter := &atomic.Int64{}
	bundle := BuildSharedBuiltin(f.pool, testEngine, f.tenant.ID, f.appID, f.userID, nil, nil, counter)

	src := `
def main():
    return shared("utils", "echo", v="hi")
`
	result, _, err := runWithShared(t, ctx, src, bundle, counter)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.(string) != "hi" {
		t.Fatalf("result = %v, want hi", result)
	}

	var after int
	if err := f.pool.QueryRow(ctx, `SELECT COUNT(*) FROM script_runs WHERE tenant_id = $1`, f.tenant.ID).Scan(&after); err != nil {
		t.Fatalf("count runs after: %v", err)
	}
	if after != before {
		t.Fatalf("script_runs delta = %d, want 0 (shared must not open run rows)", after-before)
	}
}

// TestSharedBuiltin_PositionalTargetArgsRejected: admin calls
// shared("utils", "f", "raw") — positional target args are not supported in
// v0.1. In this build Monty drops positional args beyond the registered
// param set, so we actually hit "missing required argument" from the TARGET
// function. That's still a clear error to the admin — the message contains
// enough to diagnose. Documents the v0.1 limitation.
func TestSharedBuiltin_PositionalTargetArgsRejected(t *testing.T) {
	f := newItemFixture(t)
	ctx, cancel := newCtx(t)
	defer cancel()

	insertHelperScript(t, f, f.appID, "utils", `
def f(phone):
    return phone
`)

	counter := &atomic.Int64{}
	bundle := BuildSharedBuiltin(f.pool, testEngine, f.tenant.ID, f.appID, f.userID, nil, nil, counter)

	src := `
def main():
    return shared("utils", "f", "8885551234")
`
	_, _, err := runWithShared(t, ctx, src, bundle, counter)
	if err == nil {
		t.Fatal("expected error — positional target args unsupported in v0.1")
	}
	// Either our own "positional arguments not supported" OR Monty's
	// downstream "missing required argument phone" — both diagnose the
	// issue clearly. Accept either.
	msg := err.Error()
	if !strings.Contains(msg, "positional") && !strings.Contains(msg, "phone") {
		t.Fatalf("error should mention positional-args or missing phone arg, got %v", err)
	}
}
