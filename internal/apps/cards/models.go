package cards

import (
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
// terminal states remove them.
type CardState string

const (
	CardStatePending   CardState = "pending"
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
	case CardStatePending, CardStateResolved, CardStateArchived,
		CardStateDismissed, CardStateSaved, CardStateCancelled:
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
}

// DecisionOption is a single option a user can pick when resolving a decision.
type DecisionOption struct {
	OptionID  string `json:"option_id"`
	SortOrder int    `json:"sort_order"`
	Label     string `json:"label"`
	// Prompt is the markdown description fed to the agent when this option is
	// chosen. Empty means noop — resolving records the choice but doesn't
	// trigger an agent task.
	Prompt string `json:"prompt,omitempty"`
}

// BriefingData holds briefing-specific fields.
type BriefingData struct {
	Severity BriefingSeverity `json:"severity"`
}

// CardCreateInput groups the fields needed to create a card. The Decision
// and Briefing sub-structs must match Kind.
type CardCreateInput struct {
	Kind       CardKind
	Title      string
	Body       string
	RoleScopes []string // [] means [{tenant, *}] default

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
	// removes all scope rows (making the card invisible — caller beware).
	RoleScopes *[]string
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
