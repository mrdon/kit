package chat

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
)

func evt(kind models.SessionEventType, data any) models.SessionEvent {
	raw, _ := json.Marshal(data)
	return models.SessionEvent{ID: uuid.New(), EventType: kind, Data: raw}
}

// A reload rebuilds the transcript from events: user text without the
// attachment manifest, replies joined, tool calls counted per turn.
func TestTurnsFromEvents(t *testing.T) {
	turns := turnsFromEvents([]models.SessionEvent{
		evt(models.EventTypeMessageReceived, map[string]any{"text": "add a football photo\n\n[Attached files — call read_attachment:\n- a.jpg id=1]"}),
		evt(models.EventTypeAssistantTurn, map[string]any{"content": []map[string]any{{"type": "thinking"}, {"type": "tool_use", "name": "search_photos"}}}),
		evt(models.EventTypeToolResults, map[string]any{"content": []any{}}),
		evt(models.EventTypeAssistantTurn, map[string]any{"content": []map[string]any{{"type": "tool_use", "name": "reply_in_thread"}}}),
		evt(models.EventTypeMessageSent, map[string]any{"text": "The library has no football photo."}),
		evt(models.EventTypeMessageReceived, map[string]any{"text": "try stock"}),
		evt(models.EventTypeMessageSent, map[string]any{"text": "Done."}),
	})
	if len(turns) != 2 {
		t.Fatalf("turns = %d", len(turns))
	}
	if turns[0].UserText != "add a football photo" || turns[0].Response != "The library has no football photo." || turns[0].ToolCount != 2 {
		t.Fatalf("turn 0 = %+v", turns[0])
	}
	if turns[1].UserText != "try stock" || turns[1].Response != "Done." || turns[1].ToolCount != 0 {
		t.Fatalf("turn 1 = %+v", turns[1])
	}
}
