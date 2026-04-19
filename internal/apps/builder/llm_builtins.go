// Package builder: llm_builtins.go bridges the Anthropic client into the
// Monty runtime as a flat allowlist of `llm_*` host functions.
//
// Monty's host-function ABI is a flat allowlist (no attribute dispatch on
// host objects), so we expose four top-level calls:
//
//	label  = llm_classify(text, categories, model=None)            # -> string
//	fields = llm_extract(text, schema)                              # -> dict
//	sum    = llm_summarize(text, max_words=60, model=None)          # -> string
//	out    = llm_generate(prompt, max_tokens=200, model=None, schema=None)
//	                                                                # -> string or dict
//
// Every call: looks up today's spend against the tenant's `llm_daily_cent_cap`,
// refuses if the cap is already consumed, dispatches a Claude Messages API
// request at the appropriate tier, and records a row in `llm_call_log` with
// args hash, token counts, and rounded-up cost.
//
// The bridge lives in the builder package (not runtime/) so it can import
// the anthropic client without a runtime->builder cycle. Tests inject a
// stub by passing a custom `Sender` — BuildLLMBuiltins accepts a
// `*anthropic.Client` directly so production callers don't touch the
// abstraction.
package builder

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/anthropic"
	"github.com/mrdon/kit/internal/apps/builder/runtime"
)

// Canonical builtin names. Exported so call sites (tests, registrations)
// can reference them without hard-coding strings.
const (
	FnLLMClassify  = "llm_classify"
	FnLLMExtract   = "llm_extract"
	FnLLMSummarize = "llm_summarize"
	FnLLMGenerate  = "llm_generate"
)

// Model tier keys admins pass via the `model` kwarg. Values are the actual
// Claude model IDs from Anthropic. Kept in sync with
// internal/agent/agent.go and internal/ingest/converter.go.
const (
	tierHaiku  = "haiku"
	tierSonnet = "sonnet"
	tierOpus   = "opus"

	modelIDHaiku  = "claude-haiku-4-5-20251001"
	modelIDSonnet = "claude-sonnet-4-5-20241022"
	modelIDOpus   = "claude-opus-4-1-20250805"
)

// Sender is the slice of the Anthropic client this package needs.
// Defined here so tests can swap in a stub without pulling in real HTTP.
// *anthropic.Client satisfies it directly.
type Sender interface {
	CreateMessage(ctx context.Context, req *anthropic.Request) (*anthropic.Response, error)
}

// LLMBuiltins bundles the FuncDefs, dispatcher, and per-run counters.
// Shape mirrors DBBuiltins for consistency at call sites.
type LLMBuiltins struct {
	Funcs    []runtime.FuncDef
	Handler  runtime.ExternalFunc
	BuiltIns map[string]runtime.GoFunc
	Params   map[string][]string

	tokensUsed atomic.Int64
	costCents  atomic.Int64
}

// TokensUsed returns the running total of (input+output) tokens spent by
// this bundle across all calls in the current run.
func (l *LLMBuiltins) TokensUsed() int64 { return l.tokensUsed.Load() }

// CostCents returns the rounded-up cumulative cost in cents for this run.
func (l *LLMBuiltins) CostCents() int64 { return l.costCents.Load() }

// BuildLLMBuiltins wires an Anthropic Sender into a batch of Monty host
// functions. Every call consults `tenant_builder_config.llm_daily_cent_cap`
// and aborts if today's spend in `llm_call_log` has already met or exceeded
// it. Successful calls log a row with fn, tier, args hash, tokens, and cost.
func BuildLLMBuiltins(
	pool *pgxpool.Pool,
	sender Sender,
	tenantID uuid.UUID,
	runID *uuid.UUID,
) *LLMBuiltins {
	l := &LLMBuiltins{}

	handler := func(ctx context.Context, call *runtime.FunctionCall) (any, error) {
		if err := checkBudget(ctx, pool, tenantID); err != nil {
			return nil, err
		}
		switch call.Name {
		case FnLLMClassify:
			return l.classify(ctx, pool, sender, tenantID, runID, call)
		case FnLLMExtract:
			return l.extract(ctx, pool, sender, tenantID, runID, call)
		case FnLLMSummarize:
			return l.summarize(ctx, pool, sender, tenantID, runID, call)
		case FnLLMGenerate:
			return l.generate(ctx, pool, sender, tenantID, runID, call)
		default:
			return nil, fmt.Errorf("llm_builtins: unknown function %q", call.Name)
		}
	}

	params := llmParams()
	l.Funcs = sortedFuncs(params)
	l.Handler = handler
	l.BuiltIns = wrapBuiltIns(params, handler)
	l.Params = params
	return l
}

// llmParams returns the positional parameter names per host function.
func llmParams() map[string][]string {
	return map[string][]string{
		FnLLMClassify:  {"text", "categories", "model"},
		FnLLMExtract:   {"text", "schema"},
		FnLLMSummarize: {"text", "max_words", "model"},
		FnLLMGenerate:  {"prompt", "max_tokens", "model", "schema"},
	}
}

// sortedFuncs returns FuncDefs in stable name order for deterministic
// registration logs.
func sortedFuncs(params map[string][]string) []runtime.FuncDef {
	names := make([]string, 0, len(params))
	for n := range params {
		names = append(names, n)
	}
	sort.Strings(names)
	funcs := make([]runtime.FuncDef, 0, len(names))
	for _, n := range names {
		funcs = append(funcs, runtime.Func(n, params[n]...))
	}
	return funcs
}

// wrapBuiltIns returns one GoFunc per name that validates call.Name before
// invoking the shared handler (belt-and-braces against Capabilities.BuiltIns
// map-key mismatches).
func wrapBuiltIns(params map[string][]string, handler runtime.ExternalFunc) map[string]runtime.GoFunc {
	builtIns := map[string]runtime.GoFunc{}
	for name := range params {
		n := name
		builtIns[n] = func(ctx context.Context, call *runtime.FunctionCall) (any, error) {
			if call.Name != n {
				return nil, fmt.Errorf("llm_builtins: name mismatch %q != %q", call.Name, n)
			}
			return handler(ctx, call)
		}
	}
	return builtIns
}

// checkBudget computes today's (UTC) spend from llm_call_log and refuses
// the call when it already meets or exceeds the tenant's
// llm_daily_cent_cap. The cap is approximate because a call's real cost
// is only known post-response; we never preempt mid-call, we only refuse
// to start a new one after the cap has been hit.
func checkBudget(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) error {
	var cap int
	err := pool.QueryRow(ctx, `
		SELECT llm_daily_cent_cap
		FROM tenant_builder_config
		WHERE tenant_id = $1
	`, tenantID).Scan(&cap)
	if err != nil {
		// No config row means the tenant hasn't been initialized with a
		// cap; treat that as no quota configured and let the call proceed.
		// Admins explicitly seed tenant_builder_config to enable the cap.
		if strings.Contains(err.Error(), "no rows") {
			return nil
		}
		return fmt.Errorf("llm budget lookup: %w", err)
	}
	if cap <= 0 {
		return nil
	}
	var spent int
	err = pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(cost_cents), 0)::int
		FROM llm_call_log
		WHERE tenant_id = $1
		  AND created_at >= date_trunc('day', now() AT TIME ZONE 'UTC')
	`, tenantID).Scan(&spent)
	if err != nil {
		return fmt.Errorf("llm spend lookup: %w", err)
	}
	if spent >= cap {
		return fmt.Errorf(
			"llm budget exhausted: today's spend %d cents meets or exceeds daily cap of %d cents",
			spent, cap,
		)
	}
	return nil
}

// classify dispatches llm_classify(text, categories, model=None).
func (l *LLMBuiltins) classify(
	ctx context.Context,
	pool *pgxpool.Pool,
	sender Sender,
	tenantID uuid.UUID,
	runID *uuid.UUID,
	call *runtime.FunctionCall,
) (any, error) {
	text, err := argString(call.Args, "text")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	rawCats, ok := call.Args["categories"]
	if !ok || rawCats == nil {
		return nil, fmt.Errorf("%s: missing required argument %q", call.Name, "categories")
	}
	cats, err := asStringList(rawCats)
	if err != nil {
		return nil, fmt.Errorf("%s: categories: %w", call.Name, err)
	}
	if len(cats) == 0 {
		return nil, fmt.Errorf("%s: categories must be non-empty", call.Name)
	}
	tier := resolveTier(call.Args, tierHaiku)
	prompt := fmt.Sprintf(
		"Classify the following text into exactly one of these categories: %s.\n"+
			"Respond with ONLY the category name, nothing else.\n\nText:\n%s",
		strings.Join(cats, ", "), text,
	)
	resp, err := sendUserMessage(ctx, sender, tier, 50, prompt)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	raw := strings.TrimSpace(resp.TextContent())
	// Normalize: match against provided categories (case-insensitive).
	label := matchCategory(raw, cats)
	if label == "" {
		return nil, fmt.Errorf("%s: model returned %q which is not in categories %v", call.Name, raw, cats)
	}
	l.recordCost(resp, tier)
	if lerr := logLLMCall(ctx, pool, tenantID, runID, LLMFnClassify, tier,
		map[string]any{"text": text, "categories": cats}, label, resp, costCents(resp, tier)); lerr != nil {
		return nil, fmt.Errorf("%s: logging call: %w", call.Name, lerr)
	}
	return label, nil
}

// extract dispatches llm_extract(text, schema). The schema is a dict
// describing the expected output shape; for v0.1 we prompt-engineer JSON
// output and Unmarshal the response.
// TODO(v0.2): migrate to Anthropic's native structured output once the
// current client exposes it.
func (l *LLMBuiltins) extract(
	ctx context.Context,
	pool *pgxpool.Pool,
	sender Sender,
	tenantID uuid.UUID,
	runID *uuid.UUID,
	call *runtime.FunctionCall,
) (any, error) {
	text, err := argString(call.Args, "text")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	schema, err := argMap(call.Args, "schema")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	tier := tierHaiku
	schemaJSON, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("%s: encoding schema: %w", call.Name, err)
	}
	prompt := fmt.Sprintf(
		"Extract a JSON object matching this schema from the text.\n"+
			"Schema: %s\n"+
			"Respond with ONLY a JSON object, no prose, no code fences.\n\nText:\n%s",
		string(schemaJSON), text,
	)
	resp, err := sendUserMessage(ctx, sender, tier, 1024, prompt)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	out, err := parseJSONObject(resp.TextContent())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	l.recordCost(resp, tier)
	_ = logLLMCall(ctx, pool, tenantID, runID, LLMFnExtract, tier,
		map[string]any{"text": text, "schema": schema}, out, resp, costCents(resp, tier))
	return out, nil
}

// summarize dispatches llm_summarize(text, max_words=60, model=None).
func (l *LLMBuiltins) summarize(
	ctx context.Context,
	pool *pgxpool.Pool,
	sender Sender,
	tenantID uuid.UUID,
	runID *uuid.UUID,
	call *runtime.FunctionCall,
) (any, error) {
	text, err := argString(call.Args, "text")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	maxWords, err := argOptionalInt(call.Args, "max_words")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	if maxWords == 0 {
		maxWords = 60
	}
	tier := resolveTier(call.Args, tierHaiku)
	prompt := fmt.Sprintf(
		"Summarize the following text in %d words or fewer. Respond with only the summary.\n\nText:\n%s",
		maxWords, text,
	)
	// ~1.5 tokens/word headroom, plus a floor for very short caps.
	maxTokens := max(maxWords*3, 128)
	resp, err := sendUserMessage(ctx, sender, tier, maxTokens, prompt)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	out := strings.TrimSpace(resp.TextContent())
	l.recordCost(resp, tier)
	_ = logLLMCall(ctx, pool, tenantID, runID, LLMFnSummarize, tier,
		map[string]any{"text": text, "max_words": maxWords}, out, resp, costCents(resp, tier))
	return out, nil
}

// generate dispatches llm_generate(prompt, max_tokens=200, model=None,
// schema=None). When schema is set we prompt-engineer JSON output and
// return a dict; otherwise a string.
func (l *LLMBuiltins) generate(
	ctx context.Context,
	pool *pgxpool.Pool,
	sender Sender,
	tenantID uuid.UUID,
	runID *uuid.UUID,
	call *runtime.FunctionCall,
) (any, error) {
	prompt, err := argString(call.Args, "prompt")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	maxTokens, err := argOptionalInt(call.Args, "max_tokens")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	if maxTokens == 0 {
		maxTokens = 200
	}
	schema, err := argOptionalMap(call.Args, "schema")
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	tier := resolveTier(call.Args, tierSonnet)
	effPrompt, err := withSchemaSuffix(prompt, schema)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	resp, err := sendUserMessage(ctx, sender, tier, maxTokens, effPrompt)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", call.Name, err)
	}
	raw := strings.TrimSpace(resp.TextContent())
	var result any = raw
	if schema != nil {
		obj, pErr := parseJSONObject(raw)
		if pErr != nil {
			return nil, fmt.Errorf("%s: %w", call.Name, pErr)
		}
		result = obj
	}
	l.recordCost(resp, tier)
	args := map[string]any{"prompt": prompt, "max_tokens": maxTokens}
	if schema != nil {
		args["schema"] = schema
	}
	_ = logLLMCall(ctx, pool, tenantID, runID, LLMFnGenerate, tier,
		args, result, resp, costCents(resp, tier))
	return result, nil
}

// sendUserMessage wraps a single-turn user-prompt call to the Anthropic
// Messages API; most of the llm_* builtins hit this shape.
func sendUserMessage(ctx context.Context, sender Sender, tier string, maxTokens int, prompt string) (*anthropic.Response, error) {
	return sender.CreateMessage(ctx, &anthropic.Request{
		Model:     modelIDFor(tier),
		MaxTokens: maxTokens,
		Messages: []anthropic.Message{{
			Role:    "user",
			Content: []anthropic.Content{{Type: "text", Text: prompt}},
		}},
	})
}

// withSchemaSuffix appends a JSON-output instruction to the base prompt
// when a schema is supplied. No-op when schema is nil.
func withSchemaSuffix(prompt string, schema map[string]any) (string, error) {
	if schema == nil {
		return prompt, nil
	}
	schemaJSON, err := json.Marshal(schema)
	if err != nil {
		return "", fmt.Errorf("encoding schema: %w", err)
	}
	return fmt.Sprintf(
		"%s\n\nRespond with ONLY a JSON object matching this schema: %s\nNo prose, no code fences.",
		prompt, string(schemaJSON),
	), nil
}

// recordCost bumps the atomic run-level counters.
func (l *LLMBuiltins) recordCost(resp *anthropic.Response, tier string) {
	l.tokensUsed.Add(int64(resp.Usage.InputTokens + resp.Usage.OutputTokens))
	l.costCents.Add(int64(costCents(resp, tier)))
}

// logLLMCall inserts a row into llm_call_log. Best-effort — a log failure
// never blocks the script. Returns the error for test assertions.
func logLLMCall(
	ctx context.Context,
	pool *pgxpool.Pool,
	tenantID uuid.UUID,
	runID *uuid.UUID,
	fn, tier string,
	args any,
	result any,
	resp *anthropic.Response,
	cents int,
) error {
	argsJSON, err := json.Marshal(args)
	if err != nil {
		argsJSON = []byte("{}")
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		resultJSON = []byte("null")
	}
	hash := argsHash(fn, tier, argsJSON)
	_, err = pool.Exec(ctx, `
		INSERT INTO llm_call_log (
			tenant_id, script_run_id, fn, model_tier,
			args_hash, args_payload, result,
			tokens_in, tokens_out, cost_cents
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`,
		tenantID, runID, fn, tier,
		hash, argsJSON, resultJSON,
		resp.Usage.InputTokens, resp.Usage.OutputTokens, cents,
	)
	return err
}
