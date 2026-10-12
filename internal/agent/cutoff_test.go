package agent

import (
	"testing"

	"github.com/mrdon/kit/internal/anthropic"
)

// A reply cut off mid tool call must answer every tool_use, or the next
// request is refused with "tool_use ids were found without tool_result".
func TestCutOffReplyAnswersEveryToolUse(t *testing.T) {
	resp := &anthropic.Response{StopReason: "max_tokens", Content: []anthropic.Content{
		{Type: "thinking"},
		{Type: "tool_use", ID: "toolu_a", Name: "get_poster"},
		{Type: "tool_use", ID: "toolu_b", Name: "patch_poster_source", Input: map[string]any{"summary": "half"}},
	}}
	out := cutOffReply(resp)
	if len(out) != 2 || out[0].ToolUseID != "toolu_a" || out[1].ToolUseID != "toolu_b" || out[1].Type != "tool_result" {
		t.Fatalf("results = %+v", out)
	}

	out = cutOffReply(&anthropic.Response{StopReason: "max_tokens", Content: []anthropic.Content{{Type: "text", Text: "Here is a long"}}})
	if len(out) != 1 || out[0].Type != "text" {
		t.Fatalf("no-tool cut-off = %+v", out)
	}
}
