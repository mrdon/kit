package cards

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// baseCardQuery selects every column we need to populate a *Card, including
// the decision- and briefing-specific child columns via LEFT JOIN. Callers
// append their own WHERE / ORDER BY. Note: when a caller needs to scope
// by app_card_scopes, that JOIN goes in the caller, not here.
const baseCardQuery = `
SELECT
	c.id, c.tenant_id, c.kind, c.title, c.body, c.state,
	c.terminal_at, c.terminal_by, c.created_at, c.updated_at, c.expires_at,
	d.priority, d.recommended_option_id, d.resolved_option_id, d.resolved_job_id,
	d.origin_job_id, d.origin_session_id,
	d.is_gate_artifact, d.resolved_tool_result, d.resolved_at,
	d.resolving_deadline, d.resolve_token, d.last_error,
	b.severity
FROM app_cards c
LEFT JOIN app_card_decisions d ON d.card_id = c.id
LEFT JOIN app_card_briefings b ON b.card_id = c.id
`

// scanCardRow populates a Card from a row with the columns from baseCardQuery.
// For decision cards, Options is left nil — loadDecisionOptions fills it in a
// separate bulk query.
func scanCardRow(row pgx.Row) (*Card, error) {
	var c Card
	var priority *DecisionPriority
	var recommendedOptionID, resolvedOptionID, resolvedToolResult, lastError *string
	var resolvedTaskID, originTaskID, originSessionID, resolveToken *uuid.UUID
	var isGateArtifact *bool
	var resolvedAt, resolvingDeadline *time.Time
	var severity *BriefingSeverity
	if err := row.Scan(
		&c.ID, &c.TenantID, &c.Kind, &c.Title, &c.Body, &c.State,
		&c.TerminalAt, &c.TerminalBy, &c.CreatedAt, &c.UpdatedAt, &c.ExpiresAt,
		&priority, &recommendedOptionID, &resolvedOptionID, &resolvedTaskID,
		&originTaskID, &originSessionID,
		&isGateArtifact, &resolvedToolResult, &resolvedAt,
		&resolvingDeadline, &resolveToken, &lastError,
		&severity,
	); err != nil {
		return nil, err
	}
	switch c.Kind {
	case CardKindDecision:
		d := &DecisionData{}
		if priority != nil {
			d.Priority = *priority
		}
		if recommendedOptionID != nil {
			d.RecommendedOptionID = *recommendedOptionID
		}
		if resolvedOptionID != nil {
			d.ResolvedOptionID = *resolvedOptionID
		}
		d.ResolvedJobID = resolvedTaskID
		d.OriginJobID = originTaskID
		d.OriginSessionID = originSessionID
		if isGateArtifact != nil {
			d.IsGateArtifact = *isGateArtifact
		}
		if resolvedToolResult != nil {
			d.ResolvedToolResult = *resolvedToolResult
		}
		d.ResolvedAt = resolvedAt
		d.ResolvingDeadline = resolvingDeadline
		d.ResolveToken = resolveToken
		if lastError != nil {
			d.LastError = *lastError
		}
		c.Decision = d
	case CardKindBriefing:
		b := &BriefingData{}
		if severity != nil {
			b.Severity = *severity
		}
		c.Briefing = b
	}
	return &c, nil
}

// ExpiryFromTTLDays converts a relative shelf life in days into an absolute
// deadline. Tools take a relative TTL rather than a timestamp because an LLM
// gets "3 days from now" right far more reliably than it formats an RFC-3339
// instant in the right timezone.
//
// Non-positive means no expiry and yields nil. Fractional days are honoured,
// so 0.5 is twelve hours.
func ExpiryFromTTLDays(days float64) *time.Time {
	if days <= 0 {
		return nil
	}
	t := time.Now().Add(time.Duration(days * float64(24*time.Hour)))
	return &t
}

// ttlExpiry is the create-side form: an optional ttl_days field straight to
// a deadline. nil in, nil out, meaning the card never expires.
func ttlExpiry(days *float64) *time.Time {
	if days == nil {
		return nil
	}
	return ExpiryFromTTLDays(*days)
}

// applyTTLDays folds an optional ttl_days field into a CardUpdates. A nil
// pointer leaves the existing expiry alone; a non-positive value clears it.
func applyTTLDays(u *CardUpdates, days *float64) {
	if days == nil {
		return
	}
	if expiresAt := ExpiryFromTTLDays(*days); expiresAt != nil {
		u.ExpiresAt = expiresAt
		return
	}
	u.ClearExpiresAt = true
}

// CardIDList is a card_ids argument that accepts either a single id or an
// array of them, so there's one parameter to learn instead of a card_id /
// card_ids pair whose precedence a caller has to reason about.
type CardIDList []string

// UnmarshalJSON accepts "abc" and ["abc", "def"] alike.
func (l *CardIDList) UnmarshalJSON(data []byte) error {
	var one string
	if err := json.Unmarshal(data, &one); err == nil {
		*l = CardIDList{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return errors.New("card_ids must be a card id or an array of card ids")
	}
	*l = many
	return nil
}

// parseCardIDList turns raw card_ids strings into uuids.
//
// Every id must parse. A caller clearing a backlog needs to hear about a
// malformed id rather than have it silently dropped and get back a count
// that quietly doesn't match what they sent.
func parseCardIDList(raw []string) ([]uuid.UUID, error) {
	if len(raw) == 0 {
		return nil, errors.New("card_ids is required: a card id, or an array of card ids")
	}
	ids := make([]uuid.UUID, 0, len(raw))
	for _, s := range raw {
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, fmt.Errorf("invalid card id %q", s)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// nilIfEmptyBytes returns nil for empty JSON/byte slices so JSONB columns
// stay NULL instead of the string "null".
func nilIfEmptyBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return b
}
