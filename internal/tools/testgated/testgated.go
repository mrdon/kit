// Package testgated registers a trivial PolicyGate tool
// (_test_gated_echo) used by integration tests to exercise the
// full gate -> approve -> run -> idempotent-rerun loop without
// waiting on a production gated tool like send_email.
//
// The tool is NEVER registered into a production registry. The
// package's sole consumer is test code (grep for testgated under
// _test.go files only). Do not add an import from production paths.
package testgated

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/tools"
	"github.com/mrdon/kit/internal/tools/approval"
)

// ToolName is the registered name the test tool goes by in Registry
// and in any decision card's tool_name field.
const ToolName = "_test_gated_echo"

// Dedupe is the test handler's idempotency store. Keyed by
// approval.Token's resolveToken, it records the output returned for
// that token so a re-invocation after a scheduler sweep (or a
// simulated crash) returns the same value without re-executing. This
// is the contract that every real PolicyGate tool handler must
// implement (via a durable table); the test uses an in-memory map
// and explicitly asserts idempotency in a test case.
type Dedupe struct {
	mu      sync.Mutex
	entries map[uuid.UUID]string
}

// NewDedupe returns an empty in-memory dedupe store.
func NewDedupe() *Dedupe {
	return &Dedupe{entries: map[uuid.UUID]string{}}
}

// Replayed reports whether the given resolve token has a cached
// result. Tests assert this after a simulated retry to prove the
// handler didn't re-execute the side effect.
func (d *Dedupe) Replayed(tok uuid.UUID) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.entries[tok]
	return ok
}

// echoInput is the test tool's argument shape. Kept minimal — just a
// string to echo and an optional marker so tests can verify args
// survive revise/approve unchanged.
type echoInput struct {
	Text   string `json:"text"`
	Marker string `json:"marker,omitempty"`
}

// NewDef returns a tools.Def for _test_gated_echo bound to the given
// dedupe store. The handler pulls the resolve token off ctx (which
// Registry.Execute has already verified) and returns the cached
// result if present; otherwise records the new one and returns it.
//
// This matches the contract real gated tools must follow: handler
// gets re-entered after a sweep-recovered wedged card, so it MUST
// dedupe by resolveToken to avoid double-sending.
func NewDef(dedupe *Dedupe) tools.Def {
	return tools.Def{
		Name:          ToolName,
		Description:   "Test-only echo tool gated by PolicyGate. Not registered in production.",
		DefaultPolicy: tools.PolicyGate,
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text":   map[string]any{"type": "string"},
				"marker": map[string]any{"type": "string"},
			},
			"required": []string{"text"},
		},
		Handler: func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
			// Registry.Execute has already verified there's an
			// approval token in ctx; we pull the resolve token to
			// dedupe. A missing token here would indicate a
			// bypass in the approval chain — panic is acceptable
			// since tests should never hit this.
			_, resolveTok, ok := approval.FromCtx(ec.Ctx)
			if !ok {
				return "", errors.New("testgated: handler reached without approval token")
			}

			dedupe.mu.Lock()
			defer dedupe.mu.Unlock()
			if cached, found := dedupe.entries[resolveTok]; found {
				return cached, nil
			}
			var inp echoInput
			if err := json.Unmarshal(input, &inp); err != nil {
				return "", fmt.Errorf("testgated: parsing input: %w", err)
			}
			out := "echo: " + inp.Text
			if inp.Marker != "" {
				out += fmt.Sprintf(" [marker=%s]", inp.Marker)
			}
			dedupe.entries[resolveTok] = out
			return out, nil
		},
	}
}
