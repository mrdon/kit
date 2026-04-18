package models

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SessionEventType enumerates the kinds of events written to the
// session_events log. Use these constants at every call site — do not
// pass raw strings.
type SessionEventType string

const (
	EventTypeMessageReceived  SessionEventType = "message_received"
	EventTypeMessageSent      SessionEventType = "message_sent"
	EventTypeLLMRequest       SessionEventType = "llm_request"
	EventTypeLLMResponse      SessionEventType = "llm_response"
	EventTypeAssistantTurn    SessionEventType = "assistant_turn"
	EventTypeToolResults      SessionEventType = "tool_results"
	EventTypeError            SessionEventType = "error"
	EventTypeSessionComplete  SessionEventType = "session_complete"
	EventTypeDecisionResolved SessionEventType = "decision_resolved"
)

type SessionEvent struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	SessionID uuid.UUID
	EventType SessionEventType
	Data      json.RawMessage
	CreatedAt time.Time
}

// AppendSessionEvent appends an event to the session log.
func AppendSessionEvent(ctx context.Context, pool *pgxpool.Pool, tenantID, sessionID uuid.UUID, eventType SessionEventType, data any) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshaling event data: %w", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO session_events (id, tenant_id, session_id, event_type, data)
		VALUES ($1, $2, $3, $4, $5)
	`, uuid.New(), tenantID, sessionID, string(eventType), jsonData)
	if err != nil {
		return fmt.Errorf("inserting session event: %w", err)
	}
	return nil
}

// AppendSessionEventTx is like AppendSessionEvent but runs inside the
// caller's transaction. Used when the event must land atomically with
// other writes (e.g. decision resolution flipping card state and waking
// an origin task).
func AppendSessionEventTx(ctx context.Context, tx pgx.Tx, tenantID, sessionID uuid.UUID, eventType SessionEventType, data any) error {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshaling event data: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO session_events (id, tenant_id, session_id, event_type, data)
		VALUES ($1, $2, $3, $4, $5)
	`, uuid.New(), tenantID, sessionID, string(eventType), jsonData)
	if err != nil {
		return fmt.Errorf("inserting session event: %w", err)
	}
	return nil
}

// GetSessionEvents returns all events for a session, ordered by creation time.
func GetSessionEvents(ctx context.Context, pool *pgxpool.Pool, tenantID, sessionID uuid.UUID) ([]SessionEvent, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, tenant_id, session_id, event_type, data, created_at
		FROM session_events
		WHERE tenant_id = $1 AND session_id = $2
		ORDER BY created_at ASC
	`, tenantID, sessionID)
	if err != nil {
		return nil, fmt.Errorf("querying session events: %w", err)
	}
	defer rows.Close()

	var events []SessionEvent
	for rows.Next() {
		var evt SessionEvent
		var etype string
		if err := rows.Scan(&evt.ID, &evt.TenantID, &evt.SessionID, &etype, &evt.Data, &evt.CreatedAt); err != nil {
			return nil, fmt.Errorf("scanning session event: %w", err)
		}
		evt.EventType = SessionEventType(etype)
		events = append(events, evt)
	}
	return events, rows.Err()
}
