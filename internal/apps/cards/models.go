package cards

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// CardKind discriminates between a decision card (needs a human to pick one
// of several options) and a briefing card (informational update).
type CardKind string

const (
	CardKindDecision CardKind = "decision"
	CardKindBriefing CardKind = "briefing"
)

// CardState is the state of a card. Pending cards appear in the stack;
// terminal states remove them. Resolving is a transient state held by
// decision cards while a gated tool is mid-execution — see
// CardService.ResolveDecision.
type CardState string

const (
	CardStatePending   CardState = "pending"
	CardStateResolving CardState = "resolving" // decision: gated tool is executing
	CardStateResolved  CardState = "resolved"  // decision: a human picked an option
	CardStateArchived  CardState = "archived"  // briefing: seen, useful
	CardStateDismissed CardState = "dismissed" // briefing: seen, not useful
	CardStateSaved     CardState = "saved"     // briefing: flagged for later
	CardStateCancelled CardState = "cancelled" // decision: explicitly cancelled
)

// DecisionPriority orders decisions in the stack — "urgency".
type DecisionPriority string

const (
	DecisionPriorityLow    DecisionPriority = "low"
	DecisionPriorityMedium DecisionPriority = "medium"
	DecisionPriorityHigh   DecisionPriority = "high"
)

// BriefingSeverity orders briefings in the stack — "importance".
type BriefingSeverity string

const (
	BriefingSeverityInfo      BriefingSeverity = "info"
	BriefingSeverityNotable   BriefingSeverity = "notable"
	BriefingSeverityImportant BriefingSeverity = "important"
)

// BriefingAckKind is the terminal state chosen when acknowledging a briefing.
type BriefingAckKind string

const (
	BriefingAckArchived  BriefingAckKind = "archived"
	BriefingAckDismissed BriefingAckKind = "dismissed"
	BriefingAckSaved     BriefingAckKind = "saved"
)

// Valid returns true if v matches a known kind/state value.
func (k CardKind) Valid() bool {
	switch k {
	case CardKindDecision, CardKindBriefing:
		return true
	}
	return false
}

func (s CardState) Valid() bool {
	switch s {
	case CardStatePending, CardStateResolving, CardStateResolved,
		CardStateArchived, CardStateDismissed, CardStateSaved,
		CardStateCancelled:
		return true
	}
	return false
}

func (p DecisionPriority) Valid() bool {
	switch p {
	case DecisionPriorityLow, DecisionPriorityMedium, DecisionPriorityHigh:
		return true
	}
	return false
}

func (s BriefingSeverity) Valid() bool {
	switch s {
	case BriefingSeverityInfo, BriefingSeverityNotable, BriefingSeverityImportant:
		return true
	}
	return false
}

func (k BriefingAckKind) Valid() bool {
	switch k {
	case BriefingAckArchived, BriefingAckDismissed, BriefingAckSaved:
		return true
	}
	return false
}

// StateForAck maps a BriefingAckKind to its CardState.
func (k BriefingAckKind) State() CardState {
	return CardState(k)
}

// Card is the shared shape of both decisions and briefings. The Decision
// and Briefing sub-structs are populated based on Kind.
type Card struct {
	ID         uuid.UUID  `json:"id"`
	TenantID   uuid.UUID  `json:"tenant_id"`
	Kind       CardKind   `json:"kind"`
	Title      string     `json:"title"`
	Body       string     `json:"body"`
	State      CardState  `json:"state"`
	TerminalAt *time.Time `json:"terminal_at,omitempty"`
	TerminalBy *uuid.UUID `json:"terminal_by,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`

	// Decision is non-nil when Kind == CardKindDecision.
	Decision *DecisionData `json:"decision,omitempty"`
	// Briefing is non-nil when Kind == CardKindBriefing.
	Briefing *BriefingData `json:"briefing,omitempty"`
}

// DecisionData holds decision-specific fields.
type DecisionData struct {
	Priority            DecisionPriority `json:"priority"`
	RecommendedOptionID string           `json:"recommended_option_id,omitempty"`
	ResolvedOptionID    string           `json:"resolved_option_id,omitempty"`
	ResolvedTaskID      *uuid.UUID       `json:"resolved_task_id,omitempty"`
	// OriginTaskID and OriginSessionID, when both non-nil, wire the
	// decision back to the task-and-session that created it. Resolving
	// appends a decision_resolved event to the session and requeues the
	// task so a single workflow can span multiple decisions over time.
	OriginTaskID    *uuid.UUID       `json:"origin_task_id,omitempty"`
	OriginSessionID *uuid.UUID       `json:"origin_session_id,omitempty"`
	Options         []DecisionOption `json:"options"`

	// IsGateArtifact is true when this card was minted specifically as the
	// approval gate for a PolicyGate tool call (either auto-wrapped by
	// Registry.Execute or author-gated via create_decision with a
	// PolicyGate tool_name). ResolveDecision re-checks this before
	// executing: without it, a post-creation tamper could route a
	// PolicyAllow option's tool_name into a PolicyGate tool and run
	// through an unprotected card.
	IsGateArtifact bool `json:"is_gate_artifact,omitempty"`

	// ResolvedToolResult is the full output from the gated tool's handler,
	// captured on successful resolve. Resume-path events truncate a copy to
	// 2KB; callers needing the full value use get_decision_tool_result.
	ResolvedToolResult string `json:"resolved_tool_result,omitempty"`

	// ResolvedAt records when the tool finished executing. Distinct from
	// the parent card's terminal_at which tracks state-transition time.
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`

	// ResolvingDeadline is set when state flips to 'resolving' (now+5min).
	// The scheduler sweep flips past-deadline cards back to 'pending'.
	ResolvingDeadline *time.Time `json:"resolving_deadline,omitempty"`

	// ResolveToken is the idempotency key passed to the tool handler via
	// approval.Token. If the sweep requeues a wedged card, the handler
	// uses this to avoid double-executing a side-effectful operation.
	ResolveToken *uuid.UUID `json:"resolve_token,omitempty"`

	// LastError holds the most recent resolve-path failure message, if
	// any. Cleared on successful resolve.
	LastError string `json:"last_error,omitempty"`
}

// DecisionOption is a single option a user can pick when resolving a decision.
type DecisionOption struct {
	OptionID  string `json:"option_id"`
	SortOrder int    `json:"sort_order"`
	Label     string `json:"label"`
	// Prompt is the markdown instruction fed to the agent AFTER the
	// option's ToolName (if any) has executed. Use it for chained
	// follow-up work — e.g. "after sending, mark todo {id} complete."
	// Empty means no follow-up. If ToolName is also empty, the option is
	// a noop (Skip).
	Prompt string `json:"prompt,omitempty"`
	// ToolName names a registered tool that the resolve path invokes via
	// tools.Registry.Execute when this option is chosen. Empty means no
	// tool call — rely on Prompt alone (or noop).
	ToolName string `json:"tool_name,omitempty"`
	// ToolArguments is the JSON arg blob passed to the ToolName handler.
	// Validated structurally at create/revise time; the tool's own
	// handler enforces semantic schema.
	ToolArguments json.RawMessage `json:"tool_arguments,omitempty"`
}

// BriefingData holds briefing-specific fields.
type BriefingData struct {
	Severity BriefingSeverity `json:"severity"`
}

// CardCreateInput groups the fields needed to create a card. The Decision
// and Briefing sub-structs must match Kind.
//
// Scope semantics:
//   - Both RoleScopes and UserScopes empty → tenant-wide default.
//   - Either non-empty → the union of named principals; tenant-wide is
//     NOT added in that case. UserScopes lets a card target a single
//     user (e.g. a security tripwire DM-replacement) without exposing it
//     to their teammates.
type CardCreateInput struct {
	Kind  CardKind
	Title string
	Body  string
	// RoleScopes lists role names whose holders see this card. Empty
	// (with empty UserScopes) defaults to the tenant-wide scope.
	RoleScopes []string
	// UserScopes lists user IDs who can see this card. Use for cards
	// that should only appear in one specific user's feed (e.g. a
	// per-participant vote card). Combines with RoleScopes additively
	// — any matching scope grants visibility.
	UserScopes []uuid.UUID

	// Urgent triggers an immediate out-of-band push (Slack DM) via the
	// configured PushNotifier in addition to the swipe-stack landing.
	// Use sparingly — for events the user shouldn't have to open Kit
	// to learn about, e.g. failed-unlock alarms or session-takeover
	// tripwires. Best-effort: missing notifier just skips the push.
	Urgent bool

	// Required when Kind == CardKindDecision.
	Decision *DecisionCreateInput
	// Required when Kind == CardKindBriefing.
	Briefing *BriefingCreateInput
}

// DecisionCreateInput holds the decision-specific fields for creation.
type DecisionCreateInput struct {
	Priority            DecisionPriority
	RecommendedOptionID string
	Options             []DecisionOption
	// OriginTaskID + OriginSessionID, when both non-nil, stamp the
	// decision so resolution can wake the originating task and append to
	// the originating session. Set by the agent-side create_decision
	// handler when running a scheduled task; nil for ad-hoc callers.
	OriginTaskID    *uuid.UUID
	OriginSessionID *uuid.UUID
	// IsGateArtifact is set by the service layer (CardService.CreateDecision)
	// when any option's ToolName resolves to a PolicyGate tool, OR when
	// the registry's gate interceptor mints the card. ResolveDecision
	// refuses to execute a PolicyGate option unless this flag is true.
	// Callers normally leave this false; the service layer decides.
	IsGateArtifact bool
}

// BriefingCreateInput holds the briefing-specific fields for creation.
type BriefingCreateInput struct {
	Severity BriefingSeverity
}

// CardUpdates holds optional fields for updating a card. Only non-nil
// fields are written. Kind cannot be changed after creation.
type CardUpdates struct {
	Title *string
	Body  *string
	State *CardState

	Decision *DecisionUpdates
	Briefing *BriefingUpdates

	// If non-nil, replaces the card's scope rows with these. Empty slice
	// (or nil) removes all role-based rows. UserScopes follows the same
	// nil-vs-empty convention. Caller passing only RoleScopes leaves
	// UserScopes alone (and vice versa); to clear both, pass empty
	// slices for both.
	RoleScopes *[]string
	UserScopes *[]uuid.UUID
}

// DecisionUpdates is the decision-specific slice of CardUpdates.
type DecisionUpdates struct {
	Priority            *DecisionPriority
	RecommendedOptionID *string
	Options             *[]DecisionOption // full replacement when non-nil
}

// BriefingUpdates is the briefing-specific slice of CardUpdates.
type BriefingUpdates struct {
	Severity *BriefingSeverity
}

// CardFilters is used by list_decisions / list_briefings.
type CardFilters struct {
	State CardState
	// Priority / Severity, interpreted based on the kind the caller is
	// listing. Blank means no filter.
	Priority DecisionPriority
	Severity BriefingSeverity
}
