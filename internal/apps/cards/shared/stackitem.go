// Package shared defines the wire types exchanged between the PWA stack
// endpoint and the apps that contribute items to it. It lives in its own
// package so providers (todo, calendar, ...) can import the types without
// pulling in the cards app itself.
package shared

import (
	"encoding/json"
	"time"
)

// PriorityTier is the shared ordering primitive across every provider.
// The six tiers preserve the interleave the stack used when it was only
// decisions and briefings: high decision, medium decision, important
// briefing, low decision, notable briefing, info briefing.
type PriorityTier string

const (
	TierCritical PriorityTier = "critical"
	TierHigh     PriorityTier = "high"
	TierElevated PriorityTier = "elevated"
	TierMedium   PriorityTier = "medium"
	TierLow      PriorityTier = "low"
	TierMinimal  PriorityTier = "minimal"
)

// Rank returns the sort rank for a tier. Lower = higher up in the stack.
// Unknown tiers sort last.
func (t PriorityTier) Rank() int {
	switch t {
	case TierCritical:
		return 0
	case TierHigh:
		return 1
	case TierElevated:
		return 2
	case TierMedium:
		return 3
	case TierLow:
		return 4
	case TierMinimal:
		return 5
	}
	return 99
}

// StackItem is what the PWA renders for a single card. Wire type — Go
// and TypeScript both mirror it.
type StackItem struct {
	SourceApp    string       `json:"source_app"`
	Kind         string       `json:"kind"`
	KindLabel    string       `json:"kind_label"`
	Icon         string       `json:"icon,omitempty"`
	ID           string       `json:"id"`
	Title        string       `json:"title"`
	Body         string       `json:"body"`
	PriorityTier PriorityTier `json:"priority_tier"`
	// KindWeight is a within-tier tiebreak: decisions (0) outrank briefings
	// (1) which outrank todos (2) when the tier is identical. Server-only.
	KindWeight int             `json:"-"`
	Actions    []StackAction   `json:"actions"`
	Badges     []StackBadge    `json:"badges,omitempty"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

// StackAction is one swipe or tap affordance on a card.
type StackAction struct {
	ID        string          `json:"id"`
	Direction string          `json:"direction"` // "right" | "left" | "tap"
	Label     string          `json:"label"`
	Emoji     string          `json:"emoji"`
	Params    json.RawMessage `json:"params,omitempty"`
}

// StackBadge is a small chip on the card face (e.g. "Due tomorrow").
type StackBadge struct {
	Label string `json:"label"`
	Tone  string `json:"tone"` // "urgent" | "warn" | "info"
}

// StackPage is a single provider's paginated response.
type StackPage struct {
	Items      []StackItem `json:"items"`
	NextCursor string      `json:"next_cursor,omitempty"`
}

// DetailResponse is the item detail payload. Extras carries kind-specific
// sidecar data (decision task status, todo events) without leaking into
// the shared type.
type DetailResponse struct {
	Item   StackItem                  `json:"item"`
	Extras map[string]json.RawMessage `json:"extras,omitempty"`
}

// ActionResult tells the client how to reconcile local state after an action.
// If Item is non-nil, patch it in place. Each entry in RemovedIDs is a
// compound key "source_app:kind:id" the client should drop from its list.
type ActionResult struct {
	Item       *StackItem `json:"item,omitempty"`
	RemovedIDs []string   `json:"removed_ids,omitempty"`
}

// Key returns the compound client key for an item.
func Key(sourceApp, kind, id string) string {
	return sourceApp + ":" + kind + ":" + id
}
