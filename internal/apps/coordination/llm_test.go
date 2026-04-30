package coordination

import (
	"strings"
	"testing"
)

// TestUserDraftMessage_NoProposal verifies the conditional Negotiation
// block in user_draft_message.tmpl: with HasProposal=false the entire
// block must be omitted (no "Negotiation state" leakage). This is the
// one bit of template logic complex enough to break silently — the
// other templates are straight substitution and missingkey=error catches
// field typos at first call.
func TestUserDraftMessage_NoProposal(t *testing.T) {
	got := mustRender("user_draft_message.tmpl", map[string]any{
		"Reason":          "initial",
		"OrganizerName":   "Don",
		"ParticipantName": "",
		"Title":           "Sync",
		"DurationMinutes": 30,
		"SlotsBlob":       "  - key=A | Mon → 10:00 AM\n",
		"HasProposal":     false,
		"RoundCount":      0,
		"ProposalSummary": "",
		"ProposalTimes":   "[]",
		"Notes":           "",
		"NudgeCount":      0,
	})
	if strings.Contains(got, "Negotiation state") {
		t.Errorf("HasProposal=false should hide Negotiation block; output:\n%s", got)
	}
}
