package coordination

import (
	"context"

	"github.com/mrdon/kit/internal/anthropic"
)

// NewAppForEval constructs a CoordinationApp wired with just an LLM
// client, suitable for the eval test runner. The pool/messenger/cards
// dependencies stay nil; only the LLM-driven parser is exercised.
func NewAppForEval(llm *anthropic.Client) *CoordinationApp {
	return &CoordinationApp{llm: llm}
}

// ParseMeetingReplyForEval is the public test hook. Calls into the
// otherwise-unexported parseMeetingReply so the build-tagged eval test
// package can drive it.
func (a *CoordinationApp) ParseMeetingReplyForEval(ctx context.Context, log []MessageLogEntry, slots []Slot, organizerTZ string) (*ParsedResponse, error) {
	return a.parseMeetingReply(ctx, log, slots, organizerTZ)
}
