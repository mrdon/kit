package posters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mrdon/kit/internal/anthropic"
)

// Kit's own model use in this app is small and bounded: a handful of
// single-shot Sonnet calls that return JSON, each triggered by a person.
// Every one goes through askJSON so the model, the size cap and the parsing
// live in one place.

const llmMaxTokens = 4000

// askJSON sends one system + user message to Sonnet and decodes the JSON
// object in its reply into out. A reply wrapped in a code fence or prose
// still parses: the first balanced object is taken.
func askJSON(ctx context.Context, llm anthropic.Sender, system, user string, images []anthropic.Content, out any) error {
	if llm == nil {
		return errors.New("the model is not configured")
	}
	content := append([]anthropic.Content{}, images...)
	content = append(content, anthropic.Content{Type: "text", Text: user})
	resp, err := llm.CreateMessage(ctx, &anthropic.Request{
		Model:     anthropic.ModelSonnet(),
		MaxTokens: llmMaxTokens,
		System:    []anthropic.SystemBlock{{Type: "text", Text: system}},
		Messages:  []anthropic.Message{{Role: "user", Content: content}},
	})
	if err != nil {
		return fmt.Errorf("asking the model: %w", err)
	}
	raw := extractJSON(resp.TextContent())
	if raw == "" {
		return errors.New("the model returned no JSON")
	}
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return fmt.Errorf("the model's JSON did not parse: %w", err)
	}
	return nil
}

// extractJSON returns the first balanced {...} in s, honouring strings.
func extractJSON(s string) string {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return ""
	}
	depth, inStr, esc := 0, false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			esc = false
		case inStr && c == '\\':
			esc = true
		case c == '"':
			inStr = !inStr
		case inStr:
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}
