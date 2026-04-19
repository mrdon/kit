// Package builder: util_builtins_test.go drives the utility host functions
// (now/today/date_add/date_diff/log) through the shared Monty engine and
// asserts their cross-boundary behaviour. Shares the TestMain-built runner
// from db_builtins_test.go.
package builder

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/builder/runtime"
)

// runUtil compiles+runs a Python main() function with the supplied
// UtilBuiltins. Mirrors runScript over in db_builtins_test.go so tests stay
// uniform in shape.
func runUtil(t *testing.T, ctx context.Context, src string, bundle *UtilBuiltins) (any, runtime.Metadata, error) {
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

// TestNow_ReturnsISOString: verify now() crosses as a parseable RFC3339Nano
// string within 5 seconds of the Go-side wall clock.
func TestNow_ReturnsISOString(t *testing.T) {
	ctx, cancel := newCtx(t)
	defer cancel()

	bundle := BuildUtilBuiltins(nil, uuid.New(), nil, "UTC")
	before := time.Now().UTC()

	src := `
def main():
    return now()
`
	result, _, err := runUtil(t, ctx, src, bundle)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	s, ok := result.(string)
	if !ok {
		t.Fatalf("result type = %T, want string", result)
	}
	parsed, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("now() did not produce RFC3339Nano: %v (got %q)", err, s)
	}
	after := time.Now().UTC()
	if parsed.Before(before.Add(-5*time.Second)) || parsed.After(after.Add(5*time.Second)) {
		t.Errorf("now() = %v outside [%v, %v]", parsed, before, after)
	}
	// Zone should be UTC (suffix Z or "+00:00").
	if !strings.HasSuffix(s, "Z") {
		t.Errorf("now() should end in Z for UTC, got %q", s)
	}
}

// TestToday_UTC: empty/invalid timezone falls back to UTC.
func TestToday_UTC(t *testing.T) {
	ctx, cancel := newCtx(t)
	defer cancel()

	for _, tz := range []string{"", "Not/AReal_Zone"} {
		bundle := BuildUtilBuiltins(nil, uuid.New(), nil, tz)
		src := `
def main():
    return today()
`
		result, _, err := runUtil(t, ctx, src, bundle)
		if err != nil {
			t.Fatalf("tz=%q run: %v", tz, err)
		}
		s, ok := result.(string)
		if !ok {
			t.Fatalf("tz=%q result type = %T, want string", tz, result)
		}
		want := time.Now().UTC().Format("2006-01-02")
		if s != want {
			t.Errorf("tz=%q today() = %q, want %q", tz, s, want)
		}
	}
}

// TestToday_CallerTZ: America/Denver is UTC-6 (MDT) or UTC-7 (MST). The
// Denver date must match what the Go-side observer computes with the same
// Location, which implicitly proves the builtin threaded the tz through.
func TestToday_CallerTZ(t *testing.T) {
	ctx, cancel := newCtx(t)
	defer cancel()

	loc, err := time.LoadLocation("America/Denver")
	if err != nil {
		t.Skipf("tzdata for America/Denver not available: %v", err)
	}
	bundle := BuildUtilBuiltins(nil, uuid.New(), nil, "America/Denver")

	src := `
def main():
    return today()
`
	result, _, err := runUtil(t, ctx, src, bundle)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := result.(string)
	want := time.Now().In(loc).Format("2006-01-02")
	if got != want {
		t.Errorf("today() = %q, want %q", got, want)
	}

	// Also demonstrate the case that motivated the test: if we're in the
	// slice of UTC day that Denver thinks is the previous day (evening MT),
	// we see a different date than UTC.
	utcDate := time.Now().UTC().Format("2006-01-02")
	if got != utcDate {
		// This is fine — it shows tz threaded through. Nothing to assert
		// beyond the equality check above; just log for visibility.
		t.Logf("today() (MT) = %q differs from UTC date %q — tz threading confirmed", got, utcDate)
	}
}

// TestDateAdd_Days: +2 days.
func TestDateAdd_Days(t *testing.T) {
	ctx, cancel := newCtx(t)
	defer cancel()

	bundle := BuildUtilBuiltins(nil, uuid.New(), nil, "UTC")
	src := `
def main():
    return date_add("2026-04-18T12:00:00Z", days=2)
`
	result, _, err := runUtil(t, ctx, src, bundle)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := result.(string)
	parsed, err := time.Parse(time.RFC3339Nano, got)
	if err != nil {
		t.Fatalf("unparseable: %v", err)
	}
	want := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)
	if !parsed.Equal(want) {
		t.Errorf("date_add(+2d) = %v, want %v", parsed, want)
	}
}

// TestDateAdd_Hours: +3 hours, 30 minutes.
func TestDateAdd_Hours(t *testing.T) {
	ctx, cancel := newCtx(t)
	defer cancel()

	bundle := BuildUtilBuiltins(nil, uuid.New(), nil, "UTC")
	src := `
def main():
    return date_add("2026-04-18T12:00:00Z", hours=3, minutes=30)
`
	result, _, err := runUtil(t, ctx, src, bundle)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	parsed, err := time.Parse(time.RFC3339Nano, result.(string))
	if err != nil {
		t.Fatalf("unparseable: %v", err)
	}
	want := time.Date(2026, 4, 18, 15, 30, 0, 0, time.UTC)
	if !parsed.Equal(want) {
		t.Errorf("date_add(3h30m) = %v, want %v", parsed, want)
	}
}

// TestDateAdd_Negative: negative days rewinds.
func TestDateAdd_Negative(t *testing.T) {
	ctx, cancel := newCtx(t)
	defer cancel()

	bundle := BuildUtilBuiltins(nil, uuid.New(), nil, "UTC")
	src := `
def main():
    return date_add("2026-04-18T12:00:00Z", days=-1, hours=-2)
`
	result, _, err := runUtil(t, ctx, src, bundle)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	parsed, err := time.Parse(time.RFC3339Nano, result.(string))
	if err != nil {
		t.Fatalf("unparseable: %v", err)
	}
	want := time.Date(2026, 4, 17, 10, 0, 0, 0, time.UTC)
	if !parsed.Equal(want) {
		t.Errorf("date_add(-1d -2h) = %v, want %v", parsed, want)
	}
}

// TestDateAdd_BadInput: unparseable dt returns an error.
func TestDateAdd_BadInput(t *testing.T) {
	ctx, cancel := newCtx(t)
	defer cancel()

	bundle := BuildUtilBuiltins(nil, uuid.New(), nil, "UTC")
	src := `
def main():
    return date_add("not-a-date", days=1)
`
	_, _, err := runUtil(t, ctx, src, bundle)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "date_add") || !strings.Contains(err.Error(), "RFC3339") {
		t.Errorf("error should mention date_add and RFC3339: %v", err)
	}
}

// TestDateDiff: positive, negative, and zero deltas.
func TestDateDiff(t *testing.T) {
	ctx, cancel := newCtx(t)
	defer cancel()

	bundle := BuildUtilBuiltins(nil, uuid.New(), nil, "UTC")

	// Positive: a is 1 hour after b.
	srcPos := `
def main():
    return date_diff("2026-04-18T13:00:00Z", "2026-04-18T12:00:00Z")
`
	result, _, err := runUtil(t, ctx, srcPos, bundle)
	if err != nil {
		t.Fatalf("pos: %v", err)
	}
	if got := result.(float64); got != 3600 {
		t.Errorf("positive diff = %v, want 3600", got)
	}

	// Negative: a is 30s before b.
	srcNeg := `
def main():
    return date_diff("2026-04-18T12:00:00Z", "2026-04-18T12:00:30Z")
`
	result, _, err = runUtil(t, ctx, srcNeg, bundle)
	if err != nil {
		t.Fatalf("neg: %v", err)
	}
	if got := result.(float64); got != -30 {
		t.Errorf("negative diff = %v, want -30", got)
	}

	// Zero.
	srcZero := `
def main():
    return date_diff("2026-04-18T12:00:00Z", "2026-04-18T12:00:00Z")
`
	result, _, err = runUtil(t, ctx, srcZero, bundle)
	if err != nil {
		t.Fatalf("zero: %v", err)
	}
	if got := result.(float64); got != 0 {
		t.Errorf("zero diff = %v, want 0", got)
	}
}

// TestLog_WritesRow: fires log("info", "hello", user="jane") and asserts a
// script_logs row lands with the level/message/fields.
func TestLog_WritesRow(t *testing.T) {
	f := newItemFixture(t)
	ctx, cancel := newCtx(t)
	defer cancel()

	// The script_run_id column references script_runs(id), so we need a
	// real script_runs row for the INSERT to satisfy the FK.
	runID := createScriptRun(t, f)

	bundle := BuildUtilBuiltins(f.pool, f.tenant.ID, &runID, "UTC")

	src := `
def main():
    log("info", "hello", user="jane", count=3)
    return "ok"
`
	_, _, err := runUtil(t, ctx, src, bundle)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	var level, message string
	var fields []byte
	err = f.pool.QueryRow(ctx, `
		SELECT level, message, fields FROM script_logs
		WHERE tenant_id = $1 AND script_run_id = $2
	`, f.tenant.ID, runID).Scan(&level, &message, &fields)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if level != "info" {
		t.Errorf("level = %q, want info", level)
	}
	if message != "hello" {
		t.Errorf("message = %q, want hello", message)
	}
	// JSONB may reformat whitespace on read-back, so decode before asserting
	// rather than string-matching the on-disk representation.
	var parsed map[string]any
	if err := json.Unmarshal(fields, &parsed); err != nil {
		t.Fatalf("unmarshal fields %q: %v", string(fields), err)
	}
	if got, want := parsed["user"], "jane"; got != want {
		t.Errorf("fields[user] = %v, want %v (raw: %s)", got, want, string(fields))
	}
	// JSON numbers decode to float64 in a map[string]any.
	if got, want := parsed["count"], float64(3); got != want {
		t.Errorf("fields[count] = %v, want %v (raw: %s)", got, want, string(fields))
	}
}

// TestLog_BadLevel: unknown level is rejected before the INSERT.
func TestLog_BadLevel(t *testing.T) {
	f := newItemFixture(t)
	ctx, cancel := newCtx(t)
	defer cancel()

	runID := createScriptRun(t, f)
	bundle := BuildUtilBuiltins(f.pool, f.tenant.ID, &runID, "UTC")

	src := `
def main():
    log("trace", "nope")
    return "unreached"
`
	_, _, err := runUtil(t, ctx, src, bundle)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid level") {
		t.Errorf("error should mention invalid level: %v", err)
	}

	// No row should have been written.
	var n int
	err = f.pool.QueryRow(ctx, `SELECT COUNT(*) FROM script_logs WHERE script_run_id = $1`, runID).Scan(&n)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("unexpected log rows: %d", n)
	}
}

// TestLog_NoRunID: with runID=nil, log() returns nil without writing any DB
// row (falls back to slog).
func TestLog_NoRunID(t *testing.T) {
	f := newItemFixture(t)
	ctx, cancel := newCtx(t)
	defer cancel()

	bundle := BuildUtilBuiltins(f.pool, f.tenant.ID, nil, "UTC")

	src := `
def main():
    log("info", "hello from nowhere", foo="bar")
    return "ok"
`
	result, _, err := runUtil(t, ctx, src, bundle)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.(string) != "ok" {
		t.Errorf("result = %v, want ok", result)
	}

	// No script_logs rows for this tenant.
	var n int
	err = f.pool.QueryRow(ctx, `SELECT COUNT(*) FROM script_logs WHERE tenant_id = $1`, f.tenant.ID).Scan(&n)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("script_logs rows = %d, want 0 (runID nil should go to slog)", n)
	}
}

// createScriptRun inserts a minimal script + revision + script_run so the
// script_logs.script_run_id FK is satisfied when TestLog_WritesRow inserts.
func createScriptRun(t *testing.T, f *itemFixture) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	var scriptID uuid.UUID
	err := f.pool.QueryRow(ctx, `
		INSERT INTO scripts (tenant_id, builder_app_id, name, description, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, f.tenant.ID, f.appID, "test-script-"+uuid.NewString()[:8], "util test", f.userID).Scan(&scriptID)
	if err != nil {
		t.Fatalf("insert script: %v", err)
	}

	var revID uuid.UUID
	err = f.pool.QueryRow(ctx, `
		INSERT INTO script_revisions (script_id, body, created_by)
		VALUES ($1, $2, $3)
		RETURNING id
	`, scriptID, "def main(): pass", f.userID).Scan(&revID)
	if err != nil {
		t.Fatalf("insert script_revision: %v", err)
	}

	var runID uuid.UUID
	err = f.pool.QueryRow(ctx, `
		INSERT INTO script_runs (tenant_id, script_id, revision_id, status, triggered_by, caller_user_id)
		VALUES ($1, $2, $3, 'running', 'manual', $4)
		RETURNING id
	`, f.tenant.ID, scriptID, revID, f.userID).Scan(&runID)
	if err != nil {
		t.Fatalf("insert script_run: %v", err)
	}
	return runID
}
