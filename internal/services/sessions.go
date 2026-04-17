package services

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
)

// SessionTools defines the shared tool metadata for session inspection.
var SessionTools = []ToolMeta{
	{Name: "list_sessions", Description: "List recent agent sessions for debugging. Returns session ID, channel, thread_ts, user, and timestamps.", Schema: props(map[string]any{
		"limit": field("integer", "Max sessions to return (default 20)"),
	}), AdminOnly: true},
	{Name: "get_session_events", Description: "Get all events for a session. Returns event type, data, and timestamp for each event in chronological order.", Schema: propsReq(map[string]any{
		"session_id": field("string", "The session UUID"),
	}, "session_id"), AdminOnly: true},
}

// SessionService handles session inspection with authorization.
type SessionService struct {
	pool *pgxpool.Pool
}

// List returns recent sessions for the tenant. Admin only.
func (s *SessionService) List(ctx context.Context, c *Caller, limit int) ([]models.Session, error) {
	if !c.IsAdmin {
		return nil, ErrForbidden
	}
	return models.ListRecentSessions(ctx, s.pool, c.TenantID, limit)
}

// GetEvents returns all events for a session. Admin only.
func (s *SessionService) GetEvents(ctx context.Context, c *Caller, sessionID uuid.UUID) ([]models.SessionEvent, error) {
	if !c.IsAdmin {
		return nil, ErrForbidden
	}
	return models.GetSessionEvents(ctx, s.pool, c.TenantID, sessionID)
}

// FormatSession renders a session as a human-readable line.
func FormatSession(s *models.Session) string {
	return fmt.Sprintf("[%s] channel:%s thread:%s user:%s updated:%s",
		s.ID, s.SlackChannelID, s.SlackThreadTS, s.UserID, s.UpdatedAt.Format("2006-01-02 15:04"))
}

// FormatSessionEvent renders a session event as a human-readable line.
func FormatSessionEvent(e *models.SessionEvent) string {
	// Compact the JSON data for display
	data := string(e.Data)
	var compact json.RawMessage
	if json.Unmarshal(e.Data, &compact) == nil {
		if b, err := json.Marshal(compact); err == nil {
			data = string(b)
		}
	}
	// Truncate long data
	if len(data) > 500 {
		data = data[:500] + "..."
	}
	return fmt.Sprintf("[%s] %s %s", e.CreatedAt.Format("15:04:05"), e.EventType, data)
}

// FormatSessionEvents renders all events for display.
func FormatSessionEvents(events []models.SessionEvent) string {
	var b strings.Builder
	for _, e := range events {
		b.WriteString(FormatSessionEvent(&e))
		b.WriteByte('\n')
	}
	return b.String()
}
