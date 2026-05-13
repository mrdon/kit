// Package widget hosts the floating chat-bubble surface a tenant
// embeds on their public website. Visitors are anonymous; auth is
// per-tenant via a widget token + origin allowlist. The agent runs in
// a strict read-only mode with a fixed tool allowlist.
package widget

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/agent"
	"github.com/mrdon/kit/internal/anthropic"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/tools"
)

// AllowedTools is the fixed set of tool names the widget agent may
// invoke. Anything else is filtered out at registry build time. Order
// is irrelevant; only membership matters.
var AllowedTools = []string{
	"search_skills", "load_skill", "load_skill_file", "list_skills",
	"search_memories",
	"get_calendar_events", "list_calendars",
	"reply_in_thread", // terminal reply tool; routed through tools.Responder
}

// Service is the entrypoint for both /widget/api/open and
// /widget/api/chat. It owns token validation, session resolution, and
// the agent-loop dispatch.
type Service struct {
	pool     *pgxpool.Pool
	agentRun *agent.Agent
	limiter  *Limiter
}

// New binds a service to a connection pool, agent, and rate limiter.
func New(pool *pgxpool.Pool, a *agent.Agent, lim *Limiter) *Service {
	return &Service{pool: pool, agentRun: a, limiter: lim}
}

// AuthResult is the resolved (tenant, token) pair after a request has
// been authenticated. Carried into both Open and Chat so each method
// can record token_id on its event payload without re-hashing.
type AuthResult struct {
	Token  *models.WidgetToken
	Tenant *models.Tenant
}

// Authenticate validates the token, enforces the origin allowlist,
// and applies the rate limit. Returns (nil, error) when any check
// fails. The token's last_used_at is bumped on success (best-effort).
func (s *Service) Authenticate(ctx context.Context, plaintext, origin string) (*AuthResult, error) {
	if plaintext == "" {
		return nil, ErrTokenInvalid
	}
	hash := models.HashWidgetToken(plaintext)
	tok, err := models.FindWidgetTokenByHash(ctx, s.pool, hash)
	if err != nil {
		return nil, fmt.Errorf("looking up widget token: %w", err)
	}
	if tok == nil {
		return nil, ErrTokenInvalid
	}
	if !tok.OriginAllowed(origin) {
		return nil, ErrOriginNotAllowed
	}
	if !s.limiter.Allow(tok.ID) {
		return nil, ErrRateLimited
	}
	tenant, err := models.GetTenantByID(ctx, s.pool, tok.TenantID)
	if err != nil {
		return nil, fmt.Errorf("loading tenant: %w", err)
	}
	if tenant == nil {
		return nil, ErrTokenInvalid
	}
	if err := models.TouchWidgetTokenUsed(ctx, s.pool, tok.ID); err != nil {
		slog.Warn("touching widget token last_used_at", "token_id", tok.ID, "error", err)
	}
	return &AuthResult{Token: tok, Tenant: tenant}, nil
}

// OpenInput is the payload for /widget/api/open: when the visitor
// first clicks the bubble icon. UserAgent is truncated to 200 chars
// before storage to bound the event payload.
type OpenInput struct {
	Auth           *AuthResult
	ConversationID string
	VisitorID      string
	Origin         string
	UserAgent      string
}

// Open creates the widget session for this conversation_id if it
// doesn't exist yet and writes a widget_started event. Idempotent:
// re-clicking the bubble for an already-open conversation only
// touches updated_at on the session and skips the event write so
// counts stay clean.
func (s *Service) Open(ctx context.Context, in OpenInput) error {
	if in.Auth == nil {
		return ErrTokenInvalid
	}
	if strings.TrimSpace(in.ConversationID) == "" {
		return ErrConversationIDRequired
	}
	existed, err := models.FindSessionByThread(ctx, s.pool, in.Auth.Tenant.ID, models.WidgetChannelID, in.ConversationID)
	if err != nil {
		return fmt.Errorf("finding widget session: %w", err)
	}
	session, err := models.GetOrCreateWidgetSession(ctx, s.pool, in.Auth.Tenant.ID, in.ConversationID)
	if err != nil {
		return err
	}
	if existed != nil {
		// Already opened — don't double-log the widget_started event.
		return nil
	}
	ua := in.UserAgent
	if len(ua) > 200 {
		ua = ua[:200]
	}
	payload := map[string]any{
		"token_id":        in.Auth.Token.ID,
		"visitor_id":      in.VisitorID,
		"conversation_id": in.ConversationID,
		"origin":          in.Origin,
		"user_agent":      ua,
	}
	if err := models.AppendSessionEvent(ctx, s.pool, in.Auth.Tenant.ID, session.ID, models.EventTypeWidgetStarted, payload); err != nil {
		slog.Warn("appending widget_started event", "session_id", session.ID, "error", err)
	}
	return nil
}

// ChatInput is the payload for /widget/api/chat: one user message.
type ChatInput struct {
	Auth           *AuthResult
	ConversationID string
	Message        string
	Emit           Emitter
}

// Chat runs the agent loop for one widget message and streams events
// through the supplied emitter. Returns nil on a successful turn even
// when the agent itself declines to answer ("I don't know") — only
// transport-level errors surface up.
func (s *Service) Chat(ctx context.Context, in ChatInput) error {
	if in.Auth == nil {
		return ErrTokenInvalid
	}
	if strings.TrimSpace(in.ConversationID) == "" {
		return ErrConversationIDRequired
	}
	if strings.TrimSpace(in.Message) == "" {
		return errors.New("message is required")
	}
	session, err := models.GetOrCreateWidgetSession(ctx, s.pool, in.Auth.Tenant.ID, in.ConversationID)
	if err != nil {
		return err
	}

	emit := in.Emit
	if emit == nil {
		emit = func(EventType, any) error { return nil }
	}
	_ = emit(EventStatus, map[string]string{"status": "thinking"})

	responder := tools.FuncResponder(func(_ context.Context, text string) error {
		return emit(EventMessage, map[string]string{"text": text})
	})

	runInput := agent.RunInput{
		Tenant:             in.Auth.Tenant,
		Session:            session,
		Channel:            models.WidgetChannelID,
		ThreadTS:           in.ConversationID,
		UserText:           in.Message,
		Responder:          responder,
		Model:              anthropic.ModelSonnet,
		WidgetMode:         true,
		WidgetAllowedTools: AllowedTools,
		DropGatedTools:     true,
		OnToolCall: func(name string) {
			_ = emit(EventTool, map[string]string{"name": name})
		},
		OnIteration: func() {
			_ = emit(EventStatus, map[string]string{"status": "thinking"})
		},
	}
	if err := s.agentRun.Run(ctx, runInput); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			_ = emit(EventStatus, map[string]string{"status": "cancelled"})
			return nil
		}
		slog.Warn("widget agent run failed", "error", err, "tenant_id", in.Auth.Tenant.ID, "conversation_id", in.ConversationID)
		_ = emit(EventError, map[string]string{"message": "Something went wrong. Please try again."})
		return err
	}
	return emit(EventDone, map[string]any{})
}

// EventType discriminates SSE events the frontend listens for. Kept
// as typed constants so both the server and client share one source
// of truth (the client constants live in widget.js).
type EventType string

const (
	EventStatus  EventType = "status"
	EventTool    EventType = "tool"
	EventMessage EventType = "message"
	EventDone    EventType = "done"
	EventError   EventType = "error"
)

// Emitter is the callback the SSE writer plugs into Service.Chat.
// Errors propagate up so a closed stream stops the agent loop early.
type Emitter func(event EventType, data any) error

// Errors returned to the HTTP handler so it can pick the right status
// code. They're intentionally generic so the network surface doesn't
// leak which condition failed (token vs. origin).
var (
	ErrTokenInvalid           = errors.New("widget token is invalid or revoked")
	ErrOriginNotAllowed       = errors.New("origin is not allowed for this widget token")
	ErrRateLimited            = errors.New("widget rate limit exceeded")
	ErrConversationIDRequired = errors.New("conversation_id is required")
)

// jsonOrNil is a convenience for emitting payloads where the data is
// already a typed struct — keeps Service.Chat call sites tidy by
// avoiding repeated json.Marshal noise.
func jsonOrNil(v any) json.RawMessage { //nolint:unused // helper kept for future event shapes
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}
