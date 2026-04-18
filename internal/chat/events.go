// Package chat orchestrates a single card-scoped chat turn.
//
// A "turn" is: user text in, agent runs, response out. Voice adds a
// transcription phase before that. HTTP handlers adapt this package to
// SSE; everything here is transport-agnostic so unit tests can drive
// Execute with a plain callback.
package chat

import "github.com/mrdon/kit/internal/sse"

// EventType is the set of events emitted while a chat turn runs.
// Names match the SSE event fields the frontend subscribes to. Do not
// introduce new events as raw strings — add a constant.
type EventType = sse.EventType

const (
	// EventPartial carries a partial transcript segment during voice
	// transcription. Payload: {"text": string}.
	EventPartial EventType = "partial"

	// EventFinal carries the full final transcript when voice transcription
	// completes. Payload: {"text": string}.
	EventFinal EventType = "final"

	// EventStatus carries a high-level status update for the chat sheet's
	// status line. Payload: {"status": StatusType}.
	EventStatus EventType = "status"

	// EventTool fires at the start of each tool invocation during execute.
	// Payload: {"name": string}.
	EventTool EventType = "tool"

	// EventResponse fires when the agent calls reply_in_thread. Payload:
	// {"text": string}.
	EventResponse EventType = "response"

	// EventDone fires once at the end of a successful execute. Payload:
	// {} for now; future optional fields (e.g. removed_ids) go here.
	EventDone EventType = "done"

	// EventError is the terminal failure event. Payload:
	// {"message": string}.
	EventError EventType = "error"
)

// StatusType is the enumerated value carried by EventStatus.
type StatusType string

const (
	StatusThinking  StatusType = "thinking"
	StatusCancelled StatusType = "cancelled"
)
