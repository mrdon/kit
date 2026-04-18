package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/agent"
	"github.com/mrdon/kit/internal/apps/cards/shared"
	"github.com/mrdon/kit/internal/models"
	kitslack "github.com/mrdon/kit/internal/slack"
	"github.com/mrdon/kit/internal/tools"
	"github.com/mrdon/kit/internal/transcribe"
)

// SentinelChannel identifies chat sessions in the sessions table. It's
// intentionally not a valid Slack channel id so chat sessions can't
// collide with DM/channel threads.
const SentinelChannel = "web:chat"

// Emitter receives ordered events during Transcribe/Execute. Handlers
// wrap an SSE writer to push events to the client; tests use a slice.
type Emitter func(event EventType, data any) error

// Transcribe runs whisper over the uploaded audio and emits partial and
// final events. Returns the full joined transcript for the caller's
// convenience (equivalent to the value of the final event).
func Transcribe(ctx context.Context, t transcribe.Transcriber, audio io.Reader, mime string, emit Emitter) (string, error) {
	if t == nil {
		_ = emit(EventError, map[string]any{"message": "voice transcription is not configured"})
		return "", transcribe.ErrNotConfigured
	}
	final, err := t.Transcribe(ctx, audio, mime, func(segment string) {
		_ = emit(EventPartial, map[string]any{"text": segment})
	})
	if err != nil {
		slog.Warn("whisper transcription failed", "error", err)
		_ = emit(EventError, map[string]any{"message": "we couldn't transcribe that audio; please try again"})
		return "", err
	}
	if err := emit(EventFinal, map[string]any{"text": final}); err != nil {
		return final, err
	}
	return final, nil
}

// ExecuteInput is everything Execute needs to run one chat turn. Kept
// as a struct so the HTTP adapter doesn't have to re-order a long arg
// list when the plan evolves.
type ExecuteInput struct {
	Pool   *pgxpool.Pool
	Agent  *agent.Agent
	Slack  *kitslack.Client
	Tenant *models.Tenant
	User   *models.User
	Card   *shared.StackItem
	Text   string
}

// Execute runs one chat turn for a (tenant, user, card) triple. It
// resolves/creates a deterministic session so follow-up messages on the
// same card attach to the same conversation, wires a StreamingResponder
// + OnToolCall + OnIteration hook into the agent, and runs the agent
// loop. All stream output goes through emit.
func Execute(ctx context.Context, in ExecuteInput, emit Emitter) error {
	if in.Text == "" {
		return errors.New("text required")
	}
	if in.Card == nil {
		return errors.New("card required")
	}

	thread := threadKey(in.Card, in.User.ID)
	session, err := resolveSession(ctx, in.Pool, in.Tenant.ID, in.User.ID, thread)
	if err != nil {
		slog.Warn("resolving chat session", "error", err, "tenant_id", in.Tenant.ID, "user_id", in.User.ID)
		_ = emit(EventError, map[string]any{"message": "we couldn't open your chat session; please try again"})
		return err
	}

	// Kick off with a "thinking" marker so the UI shows a status line
	// before the first LLM round-trip returns.
	_ = emit(EventStatus, map[string]any{"status": string(StatusThinking)})

	responder := tools.FuncResponder(func(ctx context.Context, text string) error {
		return emit(EventResponse, map[string]any{"text": text})
	})

	runInput := agent.RunInput{
		Slack:    in.Slack,
		Tenant:   in.Tenant,
		User:     in.User,
		Session:  session,
		Channel:  SentinelChannel,
		ThreadTS: thread,
		UserText: in.Text,

		Responder: responder,
		OnToolCall: func(name string) {
			_ = emit(EventTool, map[string]any{"name": name})
		},
		OnIteration: func() {
			_ = emit(EventStatus, map[string]any{"status": string(StatusThinking)})
		},
		// Inject the card as a system suffix so it doesn't accumulate
		// in the replayed message history when the user sends
		// follow-ups on the same card.
		SystemSuffix: buildCardSystemSuffix(in.Card),
	}

	if err := in.Agent.Run(ctx, runInput); err != nil {
		// Client abort is expected on Stop — report it as a cancel
		// status, not an error; the handler will close the stream.
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			_ = emit(EventStatus, map[string]any{"status": string(StatusCancelled)})
			return nil
		}
		slog.Warn("chat agent run failed", "error", err, "tenant_id", in.Tenant.ID, "user_id", in.User.ID)
		_ = emit(EventError, map[string]any{"message": "something went wrong while running that; please try again"})
		return err
	}

	return emit(EventDone, map[string]any{})
}

// threadKey builds the deterministic slack_thread_ts for a (card, user)
// chat conversation. Including the user id means two users pressing the
// same card get separate conversations.
func threadKey(card *shared.StackItem, userID uuid.UUID) string {
	return fmt.Sprintf("chat-%s-%s-%s-%s", card.SourceApp, card.Kind, card.ID, userID)
}

// resolveSession looks up an existing chat session for this (card, user)
// or creates one. Always returns a session with slack_channel_id set to
// SentinelChannel and slack_thread_ts set to the deterministic key, so
// agent.rebuildHistory replays the right events on follow-ups.
func resolveSession(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID, thread string) (*models.Session, error) {
	existing, err := models.FindSessionByThread(ctx, pool, tenantID, SentinelChannel, thread)
	if err != nil {
		return nil, fmt.Errorf("finding chat session: %w", err)
	}
	if existing != nil {
		return existing, nil
	}
	return models.CreateSession(ctx, pool, tenantID, SentinelChannel, thread, userID, false)
}

// buildCardSystemSuffix renders the card the user is acting on as a
// system-prompt block, not a user message. This keeps the context out
// of rebuildHistory (which replays message_received events) so it
// doesn't accumulate when the user sends follow-ups on the same card.
func buildCardSystemSuffix(card *shared.StackItem) string {
	label := card.KindLabel
	if label == "" {
		label = card.Kind
	}
	return fmt.Sprintf(
		"## Card context\nThe user is talking to you about a card they're viewing in the stack.\n- Kind: %s\n- Title: %q\n- Compound id: %s:%s:%s\n- Body:\n%s",
		label, card.Title, card.SourceApp, card.Kind, card.ID, card.Body,
	)
}
