package netlify

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/mrdon/kit/internal/anthropic"
)

// Clarification is the gate's verdict on a user prompt. Clear == true
// means "go ahead, kick off the run." Clear == false means "ask the
// user this question first" — Question is the single short sentence
// to ask, and Suggestions are 2–3 concrete picks the LLM can offer
// the user (last one is the "just try something" escape hatch).
type Clarification struct {
	Clear       bool     `json:"clear"`
	Question    string   `json:"question"`
	Suggestions []string `json:"suggestions"`
}

// clarifierPrompt is the system rubric for the Haiku gate. Kept
// terse and example-led — Haiku is more reliable with concrete
// patterns than abstract criteria. Errs on the permissive side so
// most prompts with clear intent pass through without a clarifying
// question (clarifying every request would annoy users and is
// counter to the "just trust the agent" UX).
const clarifierPrompt = `You decide whether a website-change request is specific enough
for an AI coding agent to act on.

Return ONLY a JSON object in one of these two shapes (no prose around it):

  {"clear": true}

  {"clear": false,
   "question": "<one short sentence asking the most useful clarifying question>",
   "suggestions": ["<concrete option>", "<concrete option>", "Just try something"]}

Be PERMISSIVE. Most requests with concrete intent are clear, even if
they don't pin every detail. Default to "clear: true" unless the
request is genuinely ambiguous about WHAT to change. Examples:

  "make the banner blue"             → {"clear": true}
  "update our hours to 9-5 weekdays" → {"clear": true}
  "add a contact form"               → {"clear": true}  (default form is fine)
  "swap the hero photo with this"    → {"clear": true}
  "fix the spacing on the about page"→ {"clear": true}  (agent will judge)

  "make it look more modern"         → unclear (too many possible directions)
  "fix that thing"                   → unclear (no referent)
  "improve the website"              → unclear (no scope)

For unclear cases, the question is ONE short sentence. Suggestions
are 2-3 concrete picks, with the LAST suggestion always being
"Just try something" so the user has an easy escape.`

// clarify asks Haiku whether the prompt is concrete enough. Returns
// a Clarification verdict. On any error (rate limit, malformed JSON,
// LLM unavailable) falls back to {Clear: true} so we don't gate
// users out when the gate itself is broken — paying for a Netlify
// run is better than blocking a working request.
func clarify(ctx context.Context, client *anthropic.Client, prompt string) (*Clarification, error) {
	if client == nil {
		return nil, errors.New("anthropic client not configured")
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return &Clarification{Clear: false, Question: "What change would you like me to make?",
			Suggestions: []string{"Just try something"}}, nil
	}
	resp, err := client.CreateMessage(ctx, &anthropic.Request{
		Model:     anthropic.ModelHaiku,
		MaxTokens: 200,
		System:    []anthropic.SystemBlock{{Type: "text", Text: clarifierPrompt}},
		Messages: []anthropic.Message{
			{
				Role: "user",
				Content: []anthropic.Content{
					{Type: "text", Text: prompt},
				},
			},
		},
	})
	if err != nil {
		return nil, err
	}
	body := strings.TrimSpace(resp.TextContent())
	// Be forgiving about preamble — strip everything outside the
	// first { ... } block before parsing.
	if i := strings.Index(body, "{"); i >= 0 {
		if j := strings.LastIndex(body, "}"); j > i {
			body = body[i : j+1]
		}
	}
	var out Clarification
	if uerr := json.Unmarshal([]byte(body), &out); uerr != nil {
		// Malformed response — default to clear so we don't block.
		return &Clarification{Clear: true}, nil //nolint:nilerr // intentional fallback
	}
	return &out, nil
}
