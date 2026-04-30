package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

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

// ChatHistoryWindow bounds replayed conversation pairs on the card-chat
// path. Each long-press chat starts new per (card, user) but once
// opened a user can iterate revisions; without a bound, the session
// grows unbounded and every turn pays more context cost. 6 pairs is
// enough that a back-and-forth "make it shorter / actually make it
// longer / split into two paragraphs" stays coherent.
const ChatHistoryWindow = 6

// QuickHistoryWindow bounds replayed pairs on the quick-chat (card-less)
// path. Sessions are minted fresh per sheet-open, so the window only
// needs to cover in-open correction/approval cycles. Smaller than card
// chat because there's no card anchor to keep context tight around.
const QuickHistoryWindow = 4

// cardSuffixMaxBytes caps buildCardSystemSuffix output. The LLM sees
// this on every turn; tool_arguments are typically JSON-serialized
// markdown bodies that can be a few KB. 8KB total keeps the prompt
// bounded even for a 4-option card with a big draft. Overflow gets a
// simple in-place truncation sentinel pointing users at the card
// detail page.
const cardSuffixMaxBytes = 8 * 1024

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
	// ClientSessionID scopes a quick-chat (card-less) conversation. The
	// client mints a UUID on sheet-open and sends it with every turn, so
	// multi-turn within one open attaches to the same session but
	// closing the sheet discards it. Ignored when Card is non-nil —
	// card-scoped sessions are keyed on the card triple.
	ClientSessionID string
}

// Execute runs one chat turn for a (tenant, user, card) triple. It
// resolves/creates a deterministic session so follow-up messages on the
// same card attach to the same conversation, wires a StreamingResponder
// + OnToolCall + OnIteration hook into the agent, and runs the agent
// loop. All stream output goes through emit.
//
// When in.Card is nil this is a quick-chat turn (card-less surface in
// the feed). The session is keyed by in.ClientSessionID so a fresh
// UUID per sheet-open gives fresh history, while multiple turns within
// one open share context.
func Execute(ctx context.Context, in ExecuteInput, emit Emitter) error {
	if in.Text == "" {
		return errors.New("text required")
	}
	if in.Card == nil && in.ClientSessionID == "" {
		return errors.New("client_session_id required for quick chat")
	}

	thread := threadKey(in.Card, in.User.ID, in.ClientSessionID)
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
	}
	if in.Card != nil {
		// Inject the card as a system suffix so it doesn't accumulate
		// in the replayed message history when the user sends
		// follow-ups on the same card.
		runInput.SystemSuffix = buildCardSystemSuffix(in.Card)
		// Card-chat sessions grow one pair per revise round. Keep the
		// window small so repeated revision turns don't balloon
		// context size. Tool_results also get dropped from replay
		// (they can carry KBs of echoed revise args).
		runInput.HistoryWindow = ChatHistoryWindow
		// Drop gated tools only when chatting on a decision card. The
		// intended path there is revise_decision_option (mutates this
		// card); leaving send_email/etc. available invites the LLM to
		// mint a second gate card instead of revising the first. On
		// todos/briefings there's no parallel-card concern and the
		// registry-level gate still enforces approval, so gated tools
		// stay available — the user needs to be able to say "email X"
		// from a todo without switching surfaces.
		if in.Card.Kind == "decision" {
			runInput.DropGatedTools = true
		}
	} else {
		runInput.SystemSuffix = buildQuickSystemSuffix()
		runInput.HistoryWindow = QuickHistoryWindow
		// Use Sonnet for the quick-capture surface. Haiku was
		// frequently hallucinating — replying "Created todo X" without
		// actually calling create_todo — which defeats the whole point
		// of a capture tool. Sonnet follows tool-use instructions much
		// more reliably at the cost of a few extra cents per turn, a
		// fair trade for a surface where "it said it did but didn't"
		// is the worst failure mode.
		runInput.Model = "sonnet"
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

// threadKey builds the deterministic slack_thread_ts for a chat
// conversation. For card chat (card != nil) the key is keyed on the
// card triple + user so two users pressing the same card get separate
// conversations and follow-ups on the same card attach. For quick chat
// (card == nil) the key is keyed on user + clientSessionID so each
// sheet-open gets a fresh session but multi-turn within one open
// attaches.
func threadKey(card *shared.StackItem, userID uuid.UUID, clientSessionID string) string {
	if card == nil {
		return fmt.Sprintf("chat-quick-%s-%s", userID, clientSessionID)
	}
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
//
// The card body and every option's tool_arguments are wrapped in a
// single <untrusted>…</untrusted> fenced block. That content may come
// from upstream third-party sources (scanned emails, inbound messages)
// and could contain prompt-injection payloads; the fenced tags plus
// preamble tell the LLM not to treat it as instructions. Total suffix
// size is capped at cardSuffixMaxBytes with simple in-place
// truncation if the draft is oversized.
func buildCardSystemSuffix(card *shared.StackItem) string {
	label := card.KindLabel
	if label == "" {
		label = card.Kind
	}
	rendered := mustRender("system_card_suffix.tmpl", map[string]any{
		"KindLabel":    label,
		"Title":        card.Title,
		"SourceApp":    card.SourceApp,
		"Kind":         card.Kind,
		"ID":           card.ID,
		"Body":         card.Body,
		"OptionsBlock": renderDecisionOptionsBlock(card),
	})
	return truncateSuffix(rendered, cardSuffixMaxBytes)
}

// renderDecisionOptionsBlock formats the per-option detail block for a
// decision card, including the leading blank line that separates it
// from the card body inside the <untrusted> fence. Returns "" for
// non-decision cards or cards with no usable options metadata, in
// which case the card-suffix template renders no options section at
// all.
func renderDecisionOptionsBlock(card *shared.StackItem) string {
	if card.Kind != "decision" || len(card.Metadata) == 0 {
		return ""
	}
	type optView struct {
		OptionID      string          `json:"option_id"`
		Label         string          `json:"label"`
		ToolName      string          `json:"tool_name,omitempty"`
		ToolArguments json.RawMessage `json:"tool_arguments,omitempty"`
		Prompt        string          `json:"prompt,omitempty"`
	}
	var meta struct {
		RecommendedOptionID string    `json:"recommended_option_id"`
		Options             []optView `json:"options"`
	}
	if err := json.Unmarshal(card.Metadata, &meta); err != nil || len(meta.Options) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nOptions:\n")
	for _, o := range meta.Options {
		rec := ""
		if o.OptionID == meta.RecommendedOptionID {
			rec = " (recommended)"
		}
		fmt.Fprintf(&b, "- %s%s — label: %q", o.OptionID, rec, o.Label)
		if o.ToolName != "" {
			fmt.Fprintf(&b, ", tool: %s", o.ToolName)
		}
		b.WriteString("\n")
		if len(o.ToolArguments) > 0 {
			fmt.Fprintf(&b, "  tool_arguments:\n  %s\n", string(o.ToolArguments))
		}
		if o.Prompt != "" {
			fmt.Fprintf(&b, "  follow-up prompt: %s\n", o.Prompt)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// buildQuickSystemSuffix renders the quick-chat (card-less) guidance
// block. The surface is designed for fast capture but may also carry
// questions, clarifications, and approvals — the suffix steers toward
// action when the intent is clearly a capture and keeps responses
// terse.
//
// The examples matter: without them the LLM will cheerfully say
// "Created todo X" without actually calling create_todo. Concrete
// input→tool mappings make it pattern-match to the tool call instead.
func buildQuickSystemSuffix() string {
	return mustRender("system_quick_suffix.tmpl", nil)
}

// truncateSuffix caps s at maxBytes, appending a sentinel if it
// overflowed. Simple in-place truncation (not per-option priority
// budgeting) — the user can see the full content in the card detail
// page if they need to quote it back.
func truncateSuffix(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	suffix := fmt.Sprintf("\n…[truncated at %d bytes — see card detail page for full content]", maxBytes)
	if len(suffix) >= maxBytes {
		return suffix
	}
	return s[:maxBytes-len(suffix)] + suffix
}
