package chat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mrdon/kit/internal/apps/cards/shared"
)

// TestCardSuffix_OptionsBranches covers the one piece of template logic
// in the card suffix: whether the decision-options block appears.
// Non-decision cards must not leak "Options:"; decision cards with valid
// metadata must include the per-option detail block. Other field
// substitution typos are caught by missingkey=error at first render.
func TestCardSuffix_OptionsBranches(t *testing.T) {
	t.Run("non-decision omits Options block", func(t *testing.T) {
		got := buildCardSystemSuffix(&shared.StackItem{
			SourceApp: "todo",
			Kind:      "todo",
			KindLabel: "Todo",
			ID:        "abc",
			Title:     "Buy milk",
			Body:      "buy milk before 6pm",
		})
		if strings.Contains(got, "Options:") {
			t.Errorf("non-decision card should not render Options block; output:\n%s", got)
		}
	})

	t.Run("decision with options renders block", func(t *testing.T) {
		meta, _ := json.Marshal(map[string]any{
			"recommended_option_id": "opt-2",
			"options": []map[string]any{
				{"option_id": "opt-1", "label": "Send", "tool_name": "send_email"},
				{"option_id": "opt-2", "label": "Skip", "prompt": "decline politely"},
			},
		})
		got := buildCardSystemSuffix(&shared.StackItem{
			SourceApp: "email",
			Kind:      "decision",
			KindLabel: "Decision",
			ID:        "d1",
			Title:     "Reply?",
			Body:      "should we reply?",
			Metadata:  meta,
		})
		for _, w := range []string{"Options:", "opt-2 (recommended)", "follow-up prompt: decline politely"} {
			if !strings.Contains(got, w) {
				t.Errorf("missing %q in output:\n%s", w, got)
			}
		}
	})
}
