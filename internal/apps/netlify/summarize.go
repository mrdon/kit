package netlify

import (
	"context"
	"errors"
	"strings"

	"github.com/mrdon/kit/internal/anthropic"
)

// maxDiffBytesForSummary caps how much of the unified diff we send
// to Haiku. Big diffs blow up the input cost and don't usually
// summarize better than truncated ones — the agent's typical
// "make banner blue" change is well under 5KB anyway. 30KB is a
// generous ceiling that still keeps the input token cost trivial.
const maxDiffBytesForSummary = 30 * 1024

// summaryPrompt is the system instruction for Haiku. Kept terse —
// Haiku gives more useful summaries when the rubric is concrete.
const summaryPrompt = `You are summarizing a unified git diff for a non-technical
website owner. Output ONE OR TWO short sentences in plain English describing what
changed on the website, focused on what the user will see. Use concrete details
when possible (color values, copy changes, element moves). Avoid jargon like
"refactored", "extracted component", "added import". If the diff is empty or
contains only no-op changes, output exactly: "no visible changes".

Examples of good output:
- Changed the hero background from light grey to blue (#1d4ed8).
- Replaced the "Welcome" heading with "Welcome back!" and made it italic.
- Added a new "Sponsors" section with three logos below the footer.

Do not include preamble, quotation marks, or bullet points. Just the sentence(s).`

// summarizeDiff calls Haiku to turn a unified diff into a sentence
// or two. Returns an empty string if the diff is empty or the call
// failed — the watcher renders without a summary in that case
// rather than failing the post-back.
func summarizeDiff(ctx context.Context, client *anthropic.Client, diff string) (string, error) {
	if client == nil {
		return "", errors.New("anthropic client not configured")
	}
	diff = strings.TrimSpace(diff)
	if diff == "" {
		return "", nil
	}
	if len(diff) > maxDiffBytesForSummary {
		diff = diff[:maxDiffBytesForSummary] + "\n\n[diff truncated for summary]"
	}
	resp, err := client.CreateMessage(ctx, &anthropic.Request{
		Model:     anthropic.ModelHaiku,
		MaxTokens: 200,
		System:    []anthropic.SystemBlock{{Type: "text", Text: summaryPrompt}},
		Messages: []anthropic.Message{
			{
				Role: "user",
				Content: []anthropic.Content{
					{Type: "text", Text: "```diff\n" + diff + "\n```"},
				},
			},
		},
	})
	if err != nil {
		return "", err
	}
	out := strings.TrimSpace(resp.TextContent())
	if out == "" {
		return "", nil
	}
	// Strip surrounding quotes if Haiku ignored the rubric.
	out = strings.Trim(out, `"'`)
	return out, nil
}
