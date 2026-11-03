package cards

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/console"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/chat"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/sse"
)

// Chat from a paired device (the bar iPad): the same widget and SSE events
// as quick chat, behind the kit.chat capability, running as the device.
// People keep using /api/v1/chat/*; these mounts are for devices only.
func registerDeviceChatRoutes(mux apps.Mux, a *CardsApp) {
	if a.signer == nil {
		return
	}
	route := func(h http.HandlerFunc) http.Handler {
		return console.RequireCap(a.pool, a.signer, auth.CapKitChat, h)
	}
	mux.Handle("POST /{slug}/api/device/chat/execute", route(a.handleDeviceChatExecute))
	mux.Handle("POST /{slug}/api/device/chat/transcribe", route(a.handleChatTranscribe))
}

type deviceChatRequest struct {
	Text            string `json:"text"`
	ClientSessionID string `json:"client_session_id"`
}

// handleDeviceChatExecute runs one device chat turn. Body parsing and rate
// limits happen before the SSE stream opens, as in handleChatExecute.
func (a *CardsApp) handleDeviceChatExecute(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	if caller == nil || caller.Kind != services.CallerDevice {
		// A person passes the kit.chat check too; they chat as themselves
		// on /api/v1/chat, never as a device.
		http.Error(w, "device chat is for paired devices", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	var req deviceChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" || req.ClientSessionID == "" {
		http.Error(w, "text and client_session_id are required", http.StatusBadRequest)
		return
	}
	if !a.chatLimiter.Allow(caller.ActorID) {
		http.Error(w, "too many chat requests; please wait a moment and retry", http.StatusTooManyRequests)
		return
	}
	if !a.chatLimiter.Acquire(caller.ActorID) {
		http.Error(w, "too many requests in flight; please wait", http.StatusTooManyRequests)
		return
	}
	defer a.chatLimiter.Release(caller.ActorID)
	a.streamDeviceChat(w, r, caller, req)
}

func (a *CardsApp) streamDeviceChat(w http.ResponseWriter, r *http.Request, caller *services.Caller, req deviceChatRequest) {
	sw, err := sse.New(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer sw.Close()
	emit := chat.Emitter(sw.Emit)
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("panic in device chat", "panic", rec)
			_ = emit(chat.EventError, map[string]any{"message": "something went wrong on our side; please try again"})
		}
	}()
	if a.agent == nil {
		_ = emit(chat.EventError, map[string]any{"message": "chat is not configured on this server"})
		return
	}
	tenant, device, err := a.loadDevice(r, caller)
	if err != nil {
		slog.Warn("loading device chat context", "error", err, "actor_id", caller.ActorID)
		_ = emit(chat.EventError, map[string]any{"message": "we couldn't load this device; please try again"})
		return
	}
	in := chat.DeviceInput{Pool: a.pool, Agent: a.agent, Tenant: tenant, Device: device, Text: req.Text, ClientSessionID: req.ClientSessionID}
	_ = chat.ExecuteDevice(r.Context(), in, emit)
}

// loadDevice reads the tenant and actor rows the agent run needs.
func (a *CardsApp) loadDevice(r *http.Request, caller *services.Caller) (*models.Tenant, *models.Actor, error) {
	tenant, err := models.GetTenantByID(r.Context(), a.pool, caller.TenantID)
	if err != nil {
		return nil, nil, err
	}
	if tenant == nil {
		return nil, nil, services.ErrNotFound
	}
	device, err := models.GetActor(r.Context(), a.pool, caller.TenantID, caller.ActorID)
	if err != nil {
		return nil, nil, err
	}
	if device == nil {
		return nil, nil, services.ErrNotFound
	}
	return tenant, device, nil
}

// limitKey is who a chat rate limit counts against: the person, or the
// device for a paired device (which has no user id).
func limitKey(caller *services.Caller) uuid.UUID {
	if caller.Kind == services.CallerDevice {
		return caller.ActorID
	}
	return caller.UserID
}
