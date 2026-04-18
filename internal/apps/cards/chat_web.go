package cards

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/chat"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	kitslack "github.com/mrdon/kit/internal/slack"
	"github.com/mrdon/kit/internal/sse"
	"github.com/mrdon/kit/internal/transcribe"
)

// maxAudioUploadBytes protects the server from pathological uploads.
// Mirrors transcribe.MaxUploadBytes; kept local so the cap is visible
// in the HTTP layer.
const maxAudioUploadBytes = transcribe.MaxUploadBytes

// handleChatTranscribe accepts an audio upload and streams whisper
// segments as SSE partial events, ending with a final event (or error).
// Does not involve the agent — the client reviews the transcript before
// calling chat/execute.
//
// Body parsing, auth, and rate limits all happen BEFORE we open the SSE
// response, so these failure modes return normal 4xx/5xx responses the
// browser can log as real errors instead of mid-stream event frames.
func (a *CardsApp) handleChatTranscribe(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	if caller == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if a.transcriber == nil {
		http.Error(w, "voice transcription is not configured on this server", http.StatusServiceUnavailable)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxAudioUploadBytes)
	if err := r.ParseMultipartForm(maxAudioUploadBytes); err != nil {
		slog.Warn("parsing multipart audio",
			"error", err,
			"content_type", r.Header.Get("Content-Type"),
			"content_length", r.ContentLength,
		)
		http.Error(w, "audio upload too large or malformed", http.StatusRequestEntityTooLarge)
		return
	}
	file, header, err := r.FormFile("audio")
	if err != nil {
		slog.Warn("reading audio form field", "error", err)
		http.Error(w, "missing audio field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Per-user sliding-window + concurrency caps. Transcription spawns
	// ffmpeg and whisper-cli subprocesses, each CPU-heavy, so we gate
	// both on request rate and on simultaneous in-flight handlers.
	if !a.chatLimiter.AllowTranscribe(caller.UserID) {
		http.Error(w, "too many voice uploads; please wait a moment and retry", http.StatusTooManyRequests)
		return
	}
	if !a.chatLimiter.Acquire(caller.UserID) {
		http.Error(w, "too many requests in flight; please wait", http.StatusTooManyRequests)
		return
	}
	defer a.chatLimiter.Release(caller.UserID)

	sw, err := sse.New(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer sw.Close()
	emit := chat.Emitter(sw.Emit)

	mime := header.Header.Get("Content-Type")
	slog.Info("chat transcribe request", "user_id", caller.UserID, "content_type", mime, "size", header.Size)
	if _, err := chat.Transcribe(r.Context(), a.transcriber, file, mime, emit); err != nil {
		// chat.Transcribe has already emitted an error event.
		slog.Warn("chat transcribe failed", "error", err)
	}
}

type chatExecuteRequest struct {
	Text string `json:"text"`
}

// handleChatExecute runs one chat turn against a card and streams the
// agent's progress as SSE events: status, tool, response, done.
//
// The body is parsed BEFORE starting the SSE stream — some proxies and
// HTTP/1.1 clients don't support reading the request body after the
// response headers have been sent (bidirectional streaming), and
// MaxBytesReader's requestTooLarge signal interacts badly with an
// already-flushed response.
func (a *CardsApp) handleChatExecute(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	if caller == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Cap request body + message size. Done before SSE headers so a 413
	// shows up as a proper HTTP error to the client, not a mid-stream
	// SSE error event.
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Warn("reading chat execute body", "error", err, "content_length", r.ContentLength)
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	var req chatExecuteRequest
	if err := json.Unmarshal(body, &req); err != nil || req.Text == "" {
		http.Error(w, "text required", http.StatusBadRequest)
		return
	}
	const maxTextBytes = 8 * 1024
	if len(req.Text) > maxTextBytes {
		http.Error(w, "message too long", http.StatusRequestEntityTooLarge)
		return
	}
	slog.Info("chat execute request", "user_id", caller.UserID, "text_len", len(req.Text))

	// Rate limit + concurrency cap, still pre-stream so the client can
	// retry without parsing an SSE error frame.
	if !a.chatLimiter.Allow(caller.UserID) {
		http.Error(w, "too many chat requests; please wait a moment and retry", http.StatusTooManyRequests)
		return
	}
	if !a.chatLimiter.Acquire(caller.UserID) {
		http.Error(w, "too many requests in flight; please wait", http.StatusTooManyRequests)
		return
	}
	defer a.chatLimiter.Release(caller.UserID)

	sw, err := sse.New(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer sw.Close()
	emit := chat.Emitter(sw.Emit)

	// A panic inside any tool handler must not leave the client hanging
	// on a connection that never emits done/error. Best-effort: surface
	// a generic message (no stack traces to the browser) and log the
	// details server-side for investigation.
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("panic in chat execute", "panic", rec)
			_ = emit(chat.EventError, map[string]any{"message": "something went wrong on our side; please try again"})
		}
	}()

	if a.agent == nil || a.enc == nil {
		_ = emit(chat.EventError, map[string]any{"message": "chat is not configured on this server"})
		return
	}

	sourceApp := r.PathValue("source_app")
	kind := r.PathValue("kind")
	id := r.PathValue("id")

	p := providerByName(sourceApp)
	if p == nil {
		_ = emit(chat.EventError, map[string]any{"message": "unknown source_app"})
		return
	}
	detail, err := p.GetItem(r.Context(), caller, kind, id)
	if err != nil {
		// Surface permission/not-found clearly (caller needs to know) but
		// hide any unexpected error detail behind a generic message —
		// the real error is in the server log for investigation.
		switch {
		case errors.Is(err, services.ErrNotFound):
			_ = emit(chat.EventError, map[string]any{"message": "we couldn't find that card"})
		case errors.Is(err, services.ErrForbidden):
			_ = emit(chat.EventError, map[string]any{"message": "you don't have access to that card"})
		default:
			slog.Warn("fetching card for chat", "error", err, "card", sourceApp+":"+kind+":"+id)
			_ = emit(chat.EventError, map[string]any{"message": "we couldn't load that card; please try again"})
		}
		return
	}

	tenant, user, slackClient, err := a.resolveChatContext(r.Context(), caller)
	if err != nil {
		slog.Warn("resolving chat context", "error", err)
		_ = emit(chat.EventError, map[string]any{"message": "we couldn't load your workspace; please try again"})
		return
	}

	// Mirror the Slack path's setup-complete gate: non-admins can't
	// drive the agent until setup is done, regardless of transport.
	if !tenant.SetupComplete && !user.IsAdmin {
		_ = emit(chat.EventResponse, map[string]any{"text": "I'm still being set up — please ask your admin to finish."})
		_ = emit(chat.EventDone, map[string]any{})
		return
	}

	in := chat.ExecuteInput{
		Pool:   a.pool,
		Agent:  a.agent,
		Slack:  slackClient,
		Tenant: tenant,
		User:   user,
		Card:   &detail.Item,
		Text:   req.Text,
	}
	if err := chat.Execute(r.Context(), in, emit); err != nil {
		slog.Warn("chat execute failed",
			"error", err,
			"tenant_id", tenant.ID,
			"user_id", user.ID,
			"card", sourceApp+":"+kind+":"+id,
		)
	}
}

// resolveChatContext loads the tenant + user rows and builds a
// per-tenant Slack client so agent tools that post to Slack
// (post_to_channel, dm_user) still work for chat-initiated sessions.
func (a *CardsApp) resolveChatContext(ctx context.Context, caller *services.Caller) (*models.Tenant, *models.User, *kitslack.Client, error) {
	tenant, err := models.GetTenantByID(ctx, a.pool, caller.TenantID)
	if err != nil {
		return nil, nil, nil, err
	}
	if tenant == nil {
		return nil, nil, nil, errors.New("tenant not found")
	}
	user, err := models.GetUserByID(ctx, a.pool, tenant.ID, caller.UserID)
	if err != nil {
		return nil, nil, nil, err
	}
	if user == nil {
		return nil, nil, nil, errors.New("user not found")
	}
	botToken, err := a.enc.Decrypt(tenant.BotToken)
	if err != nil {
		return nil, nil, nil, err
	}
	return tenant, user, kitslack.NewClient(botToken), nil
}
