// Package builder: llm_helpers.go — tier→model mapping, cost rates, arg
// coercion, and JSON-from-LLM parsing, split out of llm_builtins.go to
// keep each file under the 500-line project rule.
package builder

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/mrdon/kit/internal/anthropic"
)

// resolveTier pulls a string `model` kwarg and maps it to a canonical tier.
// Falls back to the supplied default when missing/None. Unknown values are
// passed through as the default tier; admins should stick to the three
// documented names.
func resolveTier(args map[string]any, fallback string) string {
	raw, ok := args["model"]
	if !ok || raw == nil {
		return fallback
	}
	s, ok := raw.(string)
	if !ok {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(s)) {
	case tierHaiku:
		return tierHaiku
	case tierSonnet:
		return tierSonnet
	case tierOpus:
		return tierOpus
	default:
		return fallback
	}
}

// modelIDFor maps a canonical tier to the Anthropic model ID used in the
// Messages API request.
func modelIDFor(tier string) string {
	switch tier {
	case tierSonnet:
		return modelIDSonnet
	case tierOpus:
		return modelIDOpus
	default:
		return modelIDHaiku
	}
}

// asStringList coerces a Monty-supplied list of strings (arriving as []any
// of strings) into []string.
func asStringList(raw any) ([]string, error) {
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("must be a list, got %T", raw)
	}
	out := make([]string, 0, len(list))
	for i, v := range list {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("item %d must be a string, got %T", i, v)
		}
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, fmt.Errorf("item %d must be a non-empty string", i)
		}
		out = append(out, s)
	}
	return out, nil
}

// matchCategory returns the canonical category string whose name matches
// the model's raw output (case-insensitive, trimmed), or "" when nothing
// matches.
func matchCategory(raw string, cats []string) string {
	want := strings.ToLower(strings.TrimSpace(raw))
	// Strip surrounding punctuation/quotes the model sometimes adds.
	want = strings.Trim(want, ".,;:!?\"' \t\n")
	for _, c := range cats {
		if strings.ToLower(c) == want {
			return c
		}
	}
	return ""
}

// parseJSONObject tolerates responses that arrive wrapped in ```json fences
// or prefixed with commentary by locating the outermost {...} span.
func parseJSONObject(s string) (map[string]any, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	}
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end < start {
		return nil, fmt.Errorf("response is not a JSON object: %q", s)
	}
	body := s[start : end+1]
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		return nil, fmt.Errorf("decoding JSON response: %w", err)
	}
	return out, nil
}

// costCents computes the per-call charge in whole cents, rounded up.
// Rates are dollars per 1M tokens from the Anthropic pricing page:
// https://www.anthropic.com/pricing (Claude 4.5 family, late 2025).
func costCents(resp *anthropic.Response, tier string) int {
	inRate, outRate := rateFor(tier)
	usdIn := float64(resp.Usage.InputTokens) * inRate / 1_000_000
	usdOut := float64(resp.Usage.OutputTokens) * outRate / 1_000_000
	cents := (usdIn + usdOut) * 100
	return int(math.Ceil(cents))
}

// rateFor returns (input_rate_usd_per_mtok, output_rate_usd_per_mtok) for
// the tier. See https://www.anthropic.com/pricing. Update when Anthropic
// revises pricing; a v0.2 improvement is to move them into config so ops
// can adjust without a redeploy.
func rateFor(tier string) (float64, float64) {
	switch tier {
	case tierSonnet:
		return 3.0, 15.0
	case tierOpus:
		return 15.0, 75.0
	default: // haiku
		return 1.0, 5.0
	}
}

// argsHash produces a stable sha256 hex of fn||tier||canonical(args).
// The standard library marshals map keys in sorted order so the hash is
// stable for identical logical args.
func argsHash(fn, tier string, args []byte) string {
	h := sha256.New()
	h.Write([]byte(fn))
	h.Write([]byte{0})
	h.Write([]byte(tier))
	h.Write([]byte{0})
	h.Write(args)
	return hex.EncodeToString(h.Sum(nil))
}
