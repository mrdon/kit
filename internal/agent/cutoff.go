package agent

import "github.com/mrdon/kit/internal/anthropic"

// cutOffNotice tells the model why its call did not run.
const cutOffNotice = "Not run: your reply hit the output limit while writing this call, so its input was cut off. " +
	"Think less before acting and make the change in smaller pieces, one short call at a time."

// cutOffReply is the user turn that answers a reply stopped at max_tokens.
// Every tool_use in it gets a tool_result (the API refuses a tool_use
// without one) saying the call was not run; a reply with no tool calls
// gets a nudge to carry on. The truncated input is never executed: it is
// whatever the model had written when the limit hit.
func cutOffReply(resp *anthropic.Response) []anthropic.Content {
	uses := resp.ToolUses()
	if len(uses) == 0 {
		return []anthropic.Content{{Type: "text", Text: "Your reply was cut off at the output limit. Carry on, more briefly."}}
	}
	out := make([]anthropic.Content, 0, len(uses))
	for _, u := range uses {
		out = append(out, anthropic.Content{Type: "tool_result", ToolUseID: u.ID, Content: cutOffNotice})
	}
	return out
}
