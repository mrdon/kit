package widget

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
)

//go:embed static/*
var staticFS embed.FS

// Handler bundles the HTTP entry points for the widget surface. They
// live at the top level (no /{slug}/ prefix) because the tenant is
// derived from the token in the request, not the URL path.
type Handler struct {
	Service *Service
}

// NewHandler binds an HTTP handler set to a service.
func NewHandler(svc *Service) *Handler {
	return &Handler{Service: svc}
}

// Register wires the widget routes onto the given mux. Call once at
// startup after the service is constructed.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("GET /widget.js", h.staticAsset("static/widget.js", "application/javascript; charset=utf-8"))
	mux.Handle("GET /widget.css", h.staticAsset("static/widget.css", "text/css; charset=utf-8"))
	mux.HandleFunc("GET /widget/api/health", h.handleHealth)
	mux.HandleFunc("POST /widget/api/open", h.handleOpen)
	mux.HandleFunc("POST /widget/api/chat", h.handleChat)
	// Preflight for cross-origin POSTs from the host site.
	mux.HandleFunc("OPTIONS /widget/api/open", h.handleCORSPreflight)
	mux.HandleFunc("OPTIONS /widget/api/chat", h.handleCORSPreflight)
}

func (h *Handler) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte("ok"))
}

// staticAsset returns a handler that serves one embedded file with
// the right Content-Type and a long-cache header. Cache-Control is
// "no-cache" while we iterate — embed.FS is rebuilt with the binary
// so client refresh is cheap.
func (h *Handler) staticAsset(path, contentType string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		data, err := fs.ReadFile(staticFS, path)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(data)
	})
}

// chatRequest is the wire shape for /widget/api/chat. Token in the
// body (not the query string) so it doesn't end up in access logs.
type chatRequest struct {
	Token          string `json:"token"`
	ConversationID string `json:"conversation_id"`
	Message        string `json:"message"`
	VisitorID      string `json:"visitor_id"`
}

// openRequest is the wire shape for /widget/api/open.
type openRequest struct {
	Token          string `json:"token"`
	ConversationID string `json:"conversation_id"`
	VisitorID      string `json:"visitor_id"`
}

func (h *Handler) handleOpen(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	setCORSHeaders(w, origin)
	if r.Header.Get("Content-Type") == "" {
		http.Error(w, "Content-Type required", http.StatusUnsupportedMediaType)
		return
	}
	var req openRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	auth, err := h.Service.Authenticate(r.Context(), req.Token, origin)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	if err := h.Service.Open(r.Context(), OpenInput{
		Auth:           auth,
		ConversationID: req.ConversationID,
		VisitorID:      req.VisitorID,
		Origin:         origin,
		UserAgent:      r.Header.Get("User-Agent"),
	}); err != nil {
		writeAuthError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (h *Handler) handleChat(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	setCORSHeaders(w, origin)

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	auth, err := h.Service.Authenticate(r.Context(), req.Token, origin)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	// SSE headers; flush immediately so the browser hands the stream to
	// the JS reader without buffering.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	flusher.Flush()

	emit := func(event EventType, data any) error {
		payload, err := json.Marshal(data)
		if err != nil {
			return fmt.Errorf("marshaling sse data: %w", err)
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}

	if err := h.Service.Chat(r.Context(), ChatInput{
		Auth:           auth,
		ConversationID: req.ConversationID,
		Message:        req.Message,
		Emit:           emit,
	}); err != nil {
		slog.Warn("widget chat failed", "error", err)
	}
}

func (h *Handler) handleCORSPreflight(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w, r.Header.Get("Origin"))
	w.WriteHeader(http.StatusNoContent)
}

// setCORSHeaders echoes the request Origin (the service-level origin
// allowlist gates which tokens accept which origins; the echo here is
// just for browser CORS plumbing). Vary: Origin so caches don't pin
// a single origin's response for everyone.
func setCORSHeaders(w http.ResponseWriter, origin string) {
	if origin == "" {
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.Header().Set("Vary", "Origin")
}

// writeAuthError maps service-layer errors to HTTP responses. Generic
// status codes — we don't reveal which check failed.
func writeAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrRateLimited):
		w.Header().Set("Retry-After", "60")
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
	case errors.Is(err, ErrTokenInvalid), errors.Is(err, ErrOriginNotAllowed):
		http.Error(w, "forbidden", http.StatusForbidden)
	case errors.Is(err, ErrConversationIDRequired):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		slog.Warn("widget request error", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}
