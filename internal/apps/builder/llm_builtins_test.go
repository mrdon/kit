// Tests for the LLM host builtins. Each test wires a stub Sender into
// BuildLLMBuiltins so we exercise the dispatcher, budget gate, cost
// accounting, and llm_call_log writes without hitting the real Claude API.
//
// These tests run against the shared test Postgres (via testdb.Open) so
// we can assert on real rows in llm_call_log and tenant_builder_config;
// each test builds its own tenant fixture for isolation.
package builder

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestClassify asserts the happy path: the stub returns one of the
// categories verbatim, the builtin returns that category, and a log row
// lands in the DB with the right shape.
func TestClassify(t *testing.T) {
	f := newLLMFixture(t)
	stub := &stubSender{respText: "complaint", inTokens: 50, outTokens: 3}
	bundle := BuildLLMBuiltins(f.pool, stub, f.tenant.ID, f.capsRunID)

	got, err := callHandler(t, bundle, FnLLMClassify, map[string]any{
		"text":       "The delivery was late and cold.",
		"categories": []any{"complaint", "compliment", "question"},
	})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if got != "complaint" {
		t.Fatalf("classify returned %v, want %q", got, "complaint")
	}
	if stub.calls.Load() != 1 {
		t.Fatalf("stub calls = %d, want 1", stub.calls.Load())
	}
	if f.countLogRows(t) != 1 {
		t.Fatalf("expected 1 log row, got %d", f.countLogRows(t))
	}
	row := f.fetchLatestLogRow(t)
	if row.fn != LLMFnClassify || row.tier != tierHaiku {
		t.Fatalf("log row: fn=%q tier=%q, want classify/haiku", row.fn, row.tier)
	}
	if row.in != 50 || row.out != 3 {
		t.Fatalf("log tokens in/out = %d/%d, want 50/3", row.in, row.out)
	}
	if row.cents < 1 {
		t.Fatalf("log cents = %d, want >= 1 (rounded up)", row.cents)
	}
	// TokensUsed should reflect the stub's usage; CostCents should be > 0.
	if bundle.TokensUsed() != 53 {
		t.Fatalf("TokensUsed = %d, want 53", bundle.TokensUsed())
	}
	if bundle.CostCents() < 1 {
		t.Fatalf("CostCents = %d, want >= 1", bundle.CostCents())
	}
}

// TestClassify_CaseInsensitive verifies we tolerate trailing punctuation
// and capitalization in the model's response.
func TestClassify_CaseInsensitive(t *testing.T) {
	f := newLLMFixture(t)
	stub := &stubSender{respText: "Complaint.", inTokens: 10, outTokens: 2}
	bundle := BuildLLMBuiltins(f.pool, stub, f.tenant.ID, f.capsRunID)

	got, err := callHandler(t, bundle, FnLLMClassify, map[string]any{
		"text":       "x",
		"categories": []any{"complaint", "compliment"},
	})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if got != "complaint" {
		t.Fatalf("classify = %v, want complaint", got)
	}
}

// TestExtract: the stub returns a JSON object; the builtin decodes it and
// returns a map[string]any.
func TestExtract(t *testing.T) {
	f := newLLMFixture(t)
	stub := &stubSender{respText: `{"name": "Jane", "age": 40}`, inTokens: 100, outTokens: 15}
	bundle := BuildLLMBuiltins(f.pool, stub, f.tenant.ID, f.capsRunID)

	got, err := callHandler(t, bundle, FnLLMExtract, map[string]any{
		"text":   "Jane is 40 years old.",
		"schema": map[string]any{"name": "string", "age": "number"},
	})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("extract returned %T, want map", got)
	}
	if m["name"] != "Jane" {
		t.Fatalf("extract name = %v, want Jane", m["name"])
	}
	if m["age"].(float64) != 40 {
		t.Fatalf("extract age = %v, want 40", m["age"])
	}
}

// TestExtract_WithCodeFence checks that the tolerant parser strips
// Markdown fences the model sometimes adds.
func TestExtract_WithCodeFence(t *testing.T) {
	f := newLLMFixture(t)
	stub := &stubSender{respText: "```json\n{\"x\": 1}\n```", inTokens: 5, outTokens: 5}
	bundle := BuildLLMBuiltins(f.pool, stub, f.tenant.ID, f.capsRunID)

	got, err := callHandler(t, bundle, FnLLMExtract, map[string]any{
		"text":   "x",
		"schema": map[string]any{"x": "number"},
	})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if got.(map[string]any)["x"].(float64) != 1 {
		t.Fatalf("extract x = %v", got)
	}
}

// TestSummarize asserts the builtin returns the stub's text trimmed.
func TestSummarize(t *testing.T) {
	f := newLLMFixture(t)
	stub := &stubSender{respText: "  A concise summary.  ", inTokens: 200, outTokens: 6}
	bundle := BuildLLMBuiltins(f.pool, stub, f.tenant.ID, f.capsRunID)

	got, err := callHandler(t, bundle, FnLLMSummarize, map[string]any{
		"text":      "Long passage goes here...",
		"max_words": float64(30),
	})
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if got != "A concise summary." {
		t.Fatalf("summarize = %q, want %q", got, "A concise summary.")
	}
	// Haiku is the default tier; request body should carry that model.
	if !strings.Contains(stub.lastReq.Model, "haiku") {
		t.Fatalf("request.Model = %q, want haiku tier", stub.lastReq.Model)
	}
}

// TestGenerate with no schema: returns a string at the Sonnet tier (the
// default for generate).
func TestGenerate(t *testing.T) {
	f := newLLMFixture(t)
	stub := &stubSender{respText: "Here is the reply.", inTokens: 80, outTokens: 20}
	bundle := BuildLLMBuiltins(f.pool, stub, f.tenant.ID, f.capsRunID)

	got, err := callHandler(t, bundle, FnLLMGenerate, map[string]any{
		"prompt":     "Write a friendly greeting.",
		"max_tokens": float64(100),
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if got != "Here is the reply." {
		t.Fatalf("generate = %v", got)
	}
	if !strings.Contains(stub.lastReq.Model, "sonnet") {
		t.Fatalf("request.Model = %q, want sonnet tier", stub.lastReq.Model)
	}
	row := f.fetchLatestLogRow(t)
	if row.tier != tierSonnet {
		t.Fatalf("log tier = %q, want sonnet", row.tier)
	}
}

// TestGenerate_WithSchema: schema provided → result is a dict.
func TestGenerate_WithSchema(t *testing.T) {
	f := newLLMFixture(t)
	stub := &stubSender{respText: `{"status": "ok"}`, inTokens: 40, outTokens: 10}
	bundle := BuildLLMBuiltins(f.pool, stub, f.tenant.ID, f.capsRunID)

	got, err := callHandler(t, bundle, FnLLMGenerate, map[string]any{
		"prompt": "Return a status object.",
		"schema": map[string]any{"status": "string"},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("generate returned %T, want map", got)
	}
	if m["status"] != "ok" {
		t.Fatalf("generate status = %v", m["status"])
	}
}

// TestBudgetExhausted: preload llm_call_log with a row summing to 1 cent
// today and set the cap to 1; the next classify must error before hitting
// the stub.
func TestBudgetExhausted(t *testing.T) {
	f := newLLMFixture(t)
	ctx := context.Background()

	// Lower the cap to 1 cent and pre-log a spend of 1 cent for today.
	_, err := f.pool.Exec(ctx,
		`UPDATE tenant_builder_config SET llm_daily_cent_cap = 1 WHERE tenant_id = $1`,
		f.tenant.ID)
	if err != nil {
		t.Fatalf("update cap: %v", err)
	}
	_, err = f.pool.Exec(ctx, `
		INSERT INTO llm_call_log (tenant_id, fn, model_tier, args_hash, cost_cents)
		VALUES ($1, $2, $3, $4, 1)
	`, f.tenant.ID, LLMFnClassify, tierHaiku, "seed-hash")
	if err != nil {
		t.Fatalf("seed log: %v", err)
	}

	stub := &stubSender{respText: "complaint", inTokens: 10, outTokens: 2}
	bundle := BuildLLMBuiltins(f.pool, stub, f.tenant.ID, f.capsRunID)
	_, err = callHandler(t, bundle, FnLLMClassify, map[string]any{
		"text":       "x",
		"categories": []any{"complaint", "compliment"},
	})
	if err == nil {
		t.Fatal("expected budget exhausted error, got nil")
	}
	if !strings.Contains(err.Error(), "budget") {
		t.Fatalf("error does not mention budget: %v", err)
	}
	if stub.calls.Load() != 0 {
		t.Fatalf("stub.calls = %d, want 0 (budget should short-circuit)", stub.calls.Load())
	}
}

// TestBudget_NoConfigRow: a tenant without a tenant_builder_config row
// should not be blocked — treat it as no cap configured.
func TestBudget_NoConfigRow(t *testing.T) {
	f := newLLMFixture(t)
	// Remove the config row entirely.
	_, err := f.pool.Exec(context.Background(),
		`DELETE FROM tenant_builder_config WHERE tenant_id = $1`, f.tenant.ID)
	if err != nil {
		t.Fatalf("delete config: %v", err)
	}

	stub := &stubSender{respText: "complaint", inTokens: 10, outTokens: 2}
	bundle := BuildLLMBuiltins(f.pool, stub, f.tenant.ID, f.capsRunID)
	_, err = callHandler(t, bundle, FnLLMClassify, map[string]any{
		"text":       "x",
		"categories": []any{"complaint"},
	})
	if err != nil {
		t.Fatalf("expected call to succeed without config, got %v", err)
	}
}

// TestCostAccumulates: two successful calls produce two log rows and
// CostCents is the sum of the two individual costs.
func TestCostAccumulates(t *testing.T) {
	f := newLLMFixture(t)
	// Big token counts so both rounded-up costs are > 0 and distinguishable.
	stub := &stubSender{respText: "a", inTokens: 20_000, outTokens: 20_000}
	bundle := BuildLLMBuiltins(f.pool, stub, f.tenant.ID, f.capsRunID)

	_, err := callHandler(t, bundle, FnLLMSummarize, map[string]any{
		"text": "hello", "max_words": float64(10),
	})
	if err != nil {
		t.Fatalf("summarize #1: %v", err)
	}
	firstCost := bundle.CostCents()
	if firstCost < 1 {
		t.Fatalf("first cost = %d, want > 0", firstCost)
	}

	// Second call with different (larger) usage.
	stub.inTokens, stub.outTokens = 40_000, 40_000
	stub.respText = "b"
	_, err = callHandler(t, bundle, FnLLMSummarize, map[string]any{
		"text": "world", "max_words": float64(10),
	})
	if err != nil {
		t.Fatalf("summarize #2: %v", err)
	}
	if bundle.CostCents() <= firstCost {
		t.Fatalf("total cost = %d, want > first call's %d", bundle.CostCents(), firstCost)
	}
	if f.countLogRows(t) != 2 {
		t.Fatalf("expected 2 log rows, got %d", f.countLogRows(t))
	}
	// Sanity: sum of per-row cents in DB equals the tracked running total.
	var dbSum int64
	err = f.pool.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(cost_cents),0) FROM llm_call_log WHERE tenant_id=$1`,
		f.tenant.ID).Scan(&dbSum)
	if err != nil {
		t.Fatalf("sum db cents: %v", err)
	}
	if dbSum != bundle.CostCents() {
		t.Fatalf("db cents sum %d != bundle.CostCents() %d", dbSum, bundle.CostCents())
	}
}

// TestUnknownCategoryErrors: the stub returns a category not in the list;
// the builtin surfaces an error mentioning the bad output.
func TestUnknownCategoryErrors(t *testing.T) {
	f := newLLMFixture(t)
	stub := &stubSender{respText: "weird", inTokens: 5, outTokens: 2}
	bundle := BuildLLMBuiltins(f.pool, stub, f.tenant.ID, f.capsRunID)

	_, err := callHandler(t, bundle, FnLLMClassify, map[string]any{
		"text":       "x",
		"categories": []any{"a", "b"},
	})
	if err == nil {
		t.Fatal("expected error for unknown category, got nil")
	}
	if !strings.Contains(err.Error(), "weird") {
		t.Fatalf("error should mention the bad output: %v", err)
	}
}

// TestSenderError: a network error from the Sender propagates through.
func TestSenderError(t *testing.T) {
	f := newLLMFixture(t)
	stub := &stubSender{err: errors.New("boom")}
	bundle := BuildLLMBuiltins(f.pool, stub, f.tenant.ID, f.capsRunID)

	_, err := callHandler(t, bundle, FnLLMClassify, map[string]any{
		"text":       "x",
		"categories": []any{"a"},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected wrapped error to mention boom: %v", err)
	}
	// No log row should be written on failure.
	if f.countLogRows(t) != 0 {
		t.Fatalf("expected 0 log rows on failure, got %d", f.countLogRows(t))
	}
}

// TestModelOverride: admin passes model="sonnet" on classify; the request
// carries the sonnet model ID, not haiku.
func TestModelOverride(t *testing.T) {
	f := newLLMFixture(t)
	stub := &stubSender{respText: "yes", inTokens: 5, outTokens: 1}
	bundle := BuildLLMBuiltins(f.pool, stub, f.tenant.ID, f.capsRunID)

	_, err := callHandler(t, bundle, FnLLMClassify, map[string]any{
		"text":       "x",
		"categories": []any{"yes", "no"},
		"model":      "sonnet",
	})
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if !strings.Contains(stub.lastReq.Model, "sonnet") {
		t.Fatalf("expected sonnet model, got %q", stub.lastReq.Model)
	}
	row := f.fetchLatestLogRow(t)
	if row.tier != tierSonnet {
		t.Fatalf("log tier = %q, want sonnet", row.tier)
	}
}
