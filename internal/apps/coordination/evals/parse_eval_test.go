//go:build eval

// Package evals_test runs LLM evals for parseMeetingReply against the
// production Anthropic endpoint. Build-tagged so it doesn't run in
// make prepush — it costs money and depends on a live API key.
//
// Run with: go test -tags eval ./internal/apps/coordination/evals/...
// Or:       make eval-parse
package evals_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/mrdon/kit/internal/anthropic"
	"github.com/mrdon/kit/internal/apps/coordination"
)

type evalInput struct {
	CandidateSlots []coordination.Slot            `json:"candidate_slots"`
	MessageLog     []coordination.MessageLogEntry `json:"message_log"`
}

type evalExpected struct {
	Intent             string                              `json:"intent"`
	CurrentConstraints map[string]coordination.SlotVerdict `json:"current_constraints,omitempty"`
}

func TestParseMeetingReply_Evals(t *testing.T) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY not set")
	}

	llm := anthropic.NewClient(apiKey)
	app := coordination.NewAppForEval(llm)

	root := "parse_meeting_reply"
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("reading evals dir %s: %v", root, err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		caseName := e.Name()
		t.Run(caseName, func(t *testing.T) {
			runOneCase(t, app, filepath.Join(root, caseName))
		})
	}
}

func runOneCase(t *testing.T, app *coordination.CoordinationApp, dir string) {
	inputBytes, err := os.ReadFile(filepath.Join(dir, "input.json"))
	if err != nil {
		t.Fatalf("input.json: %v", err)
	}
	expectedBytes, err := os.ReadFile(filepath.Join(dir, "expected.json"))
	if err != nil {
		t.Fatalf("expected.json: %v", err)
	}

	var in evalInput
	if err := json.Unmarshal(inputBytes, &in); err != nil {
		t.Fatalf("parsing input: %v", err)
	}
	var want evalExpected
	if err := json.Unmarshal(expectedBytes, &want); err != nil {
		t.Fatalf("parsing expected: %v", err)
	}

	ctx := context.Background()
	got, err := app.ParseMeetingReplyForEval(ctx, in.MessageLog, in.CandidateSlots, "UTC")
	if err != nil {
		t.Fatalf("parser failed: %v", err)
	}

	if got.Intent != want.Intent {
		t.Errorf("intent = %q, want %q\nfull output: %+v", got.Intent, want.Intent, got)
	}

	if want.CurrentConstraints != nil {
		// Structural comparison: every expected verdict must match.
		// Extra "unspecified" entries in got are fine.
		mismatches := []string{}
		keys := make([]string, 0, len(want.CurrentConstraints))
		for k := range want.CurrentConstraints {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			gotV, ok := got.CurrentConstraints[k]
			if !ok {
				mismatches = append(mismatches, k+": missing")
				continue
			}
			if gotV != want.CurrentConstraints[k] {
				mismatches = append(mismatches, k+": got "+string(gotV)+", want "+string(want.CurrentConstraints[k]))
			}
		}
		if len(mismatches) > 0 {
			t.Errorf("constraint mismatches:\n%s\nfull got: %+v", strings.Join(mismatches, "\n"), got.CurrentConstraints)
		}
	}

	_ = reflect.DeepEqual // suppress unused warnings on minimal builds
}
