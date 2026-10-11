package agent

import (
	"testing"

	"github.com/mrdon/kit/internal/anthropic"
)

// Replayed assistant turns drop their thinking blocks: the API only needs
// them within the turn that produced them, and one that lost its text in
// storage would be rejected.
func TestSanitizeHistoryStripsThinking(t *testing.T) {
	msgs := []anthropic.Message{
		{Role: "user", Content: []anthropic.Content{{Type: "text", Text: "find a photo"}}},
		{Role: "assistant", Content: []anthropic.Content{
			{Type: "thinking"},
			{Type: "tool_use", ID: "t1", Name: "search_photos", Input: map[string]any{"query": "football"}},
		}},
		{Role: "user", Content: []anthropic.Content{{Type: "tool_result", ToolUseID: "t1", Content: "none"}}},
		{Role: "assistant", Content: []anthropic.Content{
			{Type: "thinking", Thinking: "nothing fits", Signature: "sig"},
			{Type: "text", Text: "The library has no football photo."},
		}},
	}
	clean := sanitizeHistory(msgs)
	if len(clean) != 4 {
		t.Fatalf("got %d messages, want 4", len(clean))
	}
	for i, m := range clean {
		for _, c := range m.Content {
			if c.IsThinking() {
				t.Errorf("message %d still has a thinking block", i)
			}
		}
	}
	if clean[1].Content[0].Type != "tool_use" || clean[3].Content[0].Type != "text" {
		t.Fatalf("other blocks were disturbed: %+v", clean)
	}
}
