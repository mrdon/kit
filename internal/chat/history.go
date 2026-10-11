package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
)

// HistoryTurn is one exchange as the transcript shows it: what the person
// said and what Kit replied, rebuilt from the session's events so a page
// reload shows the conversation the server is still holding.
type HistoryTurn struct {
	Key       string    `json:"key"`
	UserText  string    `json:"user_text"`
	Response  string    `json:"response"`
	ToolCount int       `json:"tool_count"`
	At        time.Time `json:"at"`
}

// attachmentManifestMarker is where the agent appends the attached-files
// list to a user message; the transcript shows the words before it.
const attachmentManifestMarker = "\n\n[Attached files"

// ScopedHistory returns the last turns of the conversation on a scoped
// subject for this user, oldest first. A turn is a message_received event
// and the message_sent replies that follow it; tool_use blocks in between
// are counted so the badge matches what the live panel showed.
func ScopedHistory(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID, app, kind, id string, limit int) ([]HistoryTurn, error) {
	thread := CardThreadKey(app, kind, id, userID)
	session, err := models.FindSessionByThread(ctx, pool, tenantID, SentinelChannel, thread)
	if err != nil {
		return nil, fmt.Errorf("finding chat session: %w", err)
	}
	if session == nil {
		return []HistoryTurn{}, nil
	}
	events, err := models.GetSessionEvents(ctx, pool, tenantID, session.ID)
	if err != nil {
		return nil, fmt.Errorf("loading chat history: %w", err)
	}
	turns := turnsFromEvents(events)
	if limit > 0 && len(turns) > limit {
		turns = turns[len(turns)-limit:]
	}
	return turns, nil
}

func turnsFromEvents(events []models.SessionEvent) []HistoryTurn {
	turns := []HistoryTurn{}
	var cur *HistoryTurn
	for i := range events {
		evt := &events[i]
		// Only three kinds shape the transcript; requests, tool results,
		// errors and the rest are not shown.
		if evt.EventType == models.EventTypeMessageReceived {
			if t, ok := receivedTurn(evt); ok {
				turns = append(turns, t)
				cur = &turns[len(turns)-1]
			}
			continue
		}
		if cur == nil {
			continue
		}
		switch evt.EventType { //nolint:exhaustive // only the kinds the transcript shows
		case models.EventTypeAssistantTurn:
			cur.ToolCount += toolUseCount(evt.Data)
		case models.EventTypeMessageSent:
			var d struct {
				Text string `json:"text"`
			}
			if json.Unmarshal(evt.Data, &d) == nil && d.Text != "" {
				if cur.Response != "" {
					cur.Response += "\n\n"
				}
				cur.Response += d.Text
			}
		}
	}
	return turns
}

func receivedTurn(evt *models.SessionEvent) (HistoryTurn, bool) {
	var d struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(evt.Data, &d) != nil || d.Text == "" {
		return HistoryTurn{}, false
	}
	if at := strings.Index(d.Text, attachmentManifestMarker); at >= 0 {
		d.Text = d.Text[:at]
	}
	return HistoryTurn{Key: evt.ID.String(), UserText: d.Text, At: evt.CreatedAt}, true
}

func toolUseCount(raw json.RawMessage) int {
	var d struct {
		Content []struct {
			Type string `json:"type"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &d) != nil {
		return 0
	}
	n := 0
	for _, c := range d.Content {
		if c.Type == "tool_use" {
			n++
		}
	}
	return n
}
