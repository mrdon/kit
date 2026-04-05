package slack

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// MessageHandler is called when a processable message or app_mention event is received.
type MessageHandler func(teamID string, event json.RawMessage, eventType string)

// Handler handles incoming Slack Events API requests.
type Handler struct {
	signingSecret string
	onMessage     MessageHandler
}

// NewHandler creates a new Slack event handler.
func NewHandler(signingSecret string, onMessage MessageHandler) *Handler {
	return &Handler{
		signingSecret: signingSecret,
		onMessage:     onMessage,
	}
}

// ServeHTTP handles Slack Events API webhook requests.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("reading request body", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Verify signature
	if !h.verifySignature(r.Header, body) {
		slog.Warn("invalid slack signature")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var outer OuterEvent
	if err := json.Unmarshal(body, &outer); err != nil {
		slog.Error("parsing event", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	switch outer.Type {
	case "url_verification":
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(outer.Challenge))
		return

	case "event_callback":
		// Respond 200 immediately — Slack retries after 3s
		w.WriteHeader(http.StatusOK)

		// Parse inner event type
		var inner InnerEvent
		if err := json.Unmarshal(outer.Event, &inner); err != nil {
			slog.Error("parsing inner event", "error", err)
			return
		}

		switch inner.Type {
		case "message":
			if inner.BotID != "" || inner.SubType != "" {
				return
			}
			go h.onMessage(outer.TeamID, outer.Event, "message")

		case "app_mention":
			go h.onMessage(outer.TeamID, outer.Event, "app_mention")

		default:
			slog.Debug("ignoring event type", "type", inner.Type)
		}

	default:
		slog.Warn("unknown outer event type", "type", outer.Type)
		http.Error(w, "unknown event type", http.StatusBadRequest)
	}
}

// verifySignature verifies the Slack request signature using HMAC-SHA256.
func (h *Handler) verifySignature(header http.Header, body []byte) bool {
	timestamp := header.Get("X-Slack-Request-Timestamp")
	signature := header.Get("X-Slack-Signature")

	if timestamp == "" || signature == "" {
		return false
	}

	// Reject requests older than 5 minutes to prevent replay attacks
	ts, err := fmt.Sscanf(timestamp, "%d", new(int64))
	if err != nil || ts != 1 {
		return false
	}
	var tsVal int64
	fmt.Sscanf(timestamp, "%d", &tsVal)
	if time.Now().Unix()-tsVal > 300 {
		return false
	}

	// Compute expected signature
	sigBase := "v0:" + timestamp + ":" + string(body)
	mac := hmac.New(sha256.New, []byte(h.signingSecret))
	mac.Write([]byte(sigBase))
	expected := "v0=" + hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expected), []byte(strings.TrimSpace(signature)))
}
