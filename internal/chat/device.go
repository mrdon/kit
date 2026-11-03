package chat

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/agent"
	"github.com/mrdon/kit/internal/anthropic"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/tools"
)

// Device chat: staff asking Kit from a paired shared screen (the bar
// iPad). The turn runs as the device, so it sees what the device's roles
// grant, and the registry is clamped to read-only answers — whoever is
// standing at the screen is not anyone in particular, so nothing they ask
// can create, change or send.

// DeviceAllowedTools is everything a device chat may call: look things up
// and reply.
var DeviceAllowedTools = []string{
	"search_skills", "load_skill", "load_skill_file", "list_skills",
	"search_memories",
	"get_calendar_events", "list_calendars",
	"reply_in_thread",
}

// DeviceInput is one device chat turn.
type DeviceInput struct {
	Pool   *pgxpool.Pool
	Agent  *agent.Agent
	Tenant *models.Tenant
	Device *models.Actor
	Text   string
	// ClientSessionID scopes the conversation to one opening of the chat
	// sheet, as in quick chat, so the next person to walk up starts clean.
	ClientSessionID string
}

// ExecuteDevice runs one chat turn for a paired device, streaming through
// emit the same events Execute does.
func ExecuteDevice(ctx context.Context, in DeviceInput, emit Emitter) error {
	if in.Text == "" {
		return errors.New("text required")
	}
	if in.ClientSessionID == "" {
		return errors.New("client_session_id required")
	}
	thread := fmt.Sprintf("chat-device-%s-%s", in.Device.ID, in.ClientSessionID)
	session, err := models.GetOrCreateDeviceSession(ctx, in.Pool, in.Tenant.ID, SentinelChannel, thread, in.Device.Label)
	if err != nil {
		slog.Warn("resolving device chat session", "error", err, "tenant_id", in.Tenant.ID, "actor_id", in.Device.ID)
		_ = emit(EventError, map[string]any{"message": "we couldn't open a chat session; please try again"})
		return err
	}

	_ = emit(EventStatus, map[string]any{"status": string(StatusThinking)})
	var usage TurnUsage
	runInput := agent.RunInput{
		Tenant:   in.Tenant,
		Device:   in.Device,
		Session:  session,
		Channel:  SentinelChannel,
		ThreadTS: thread,
		UserText: in.Text,
		Responder: tools.FuncResponder(func(_ context.Context, text string) error {
			return emit(EventResponse, map[string]any{"text": text})
		}),
		OnToolCall: func(name string) {
			_ = emit(EventTool, map[string]any{"name": name})
		},
		OnIteration: func() {
			_ = emit(EventStatus, map[string]any{"status": string(StatusThinking)})
		},
		OnUsage: usage.add,
		// A shared screen can sit on one conversation all shift; replay
		// only the last few exchanges, without tool results, so a long
		// session costs the same per question as a short one.
		HistoryWindow:      ChatHistoryWindow,
		Model:              anthropic.ModelSonnet(),
		WidgetAllowedTools: DeviceAllowedTools,
		DropGatedTools:     true,
	}
	if err := in.Agent.Run(ctx, runInput); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			_ = emit(EventStatus, map[string]any{"status": string(StatusCancelled)})
			return nil
		}
		slog.Warn("device chat run failed", "error", err, "tenant_id", in.Tenant.ID, "actor_id", in.Device.ID)
		_ = emit(EventError, map[string]any{"message": "something went wrong while running that; please try again"})
		return err
	}
	return emit(EventDone, map[string]any{"usage": usage.priced()})
}
