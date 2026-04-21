package cards

import (
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
	c.terminal_at, c.terminal_by, c.created_at, c.updated_at,
	d.priority, d.recommended_option_id, d.resolved_option_id, d.resolved_task_id,
	d.origin_task_id, d.origin_session_id,
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
		&c.TerminalAt, &c.TerminalBy, &c.CreatedAt, &c.UpdatedAt,
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
		d.ResolvedTaskID = resolvedTaskID
		d.OriginTaskID = originTaskID
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
