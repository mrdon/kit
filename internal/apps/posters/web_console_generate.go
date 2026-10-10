package posters

import (
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/sse"
)

// Generate streams progress over SSE (copy, photo, rendering, done) so the
// drawer can show where the twenty seconds are going.

type generateBody struct {
	EventID string `json:"event_id"`
	More    bool   `json:"more"`
}

func (a *App) handleGenerate(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	var body generateBody
	if !readBody(w, r, &body) {
		return
	}
	eventID, err := uuid.Parse(body.EventID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "event_id is required")
		return
	}
	if !a.RendererReady(r.Context()) {
		writeErr(w, http.StatusServiceUnavailable, "the poster renderer is not running; try again in a moment")
		return
	}
	sw, err := sse.New(w, r)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer sw.Close()
	progress := func(stage string, data map[string]any) {
		payload := map[string]any{"stage": stage}
		maps.Copy(payload, data)
		_ = sw.Emit("progress", payload)
	}
	res, err := a.Generate(r.Context(), caller.TenantID, caller.UserID, eventID, body.More, progress)
	if err != nil {
		msg := "could not generate options"
		switch {
		case errors.Is(err, ErrInvalid), errors.Is(err, ErrNoBrand):
			msg = strings.TrimPrefix(err.Error(), "invalid: ")
		case errors.Is(err, ErrNotFound):
			msg = "event not found"
		default:
			slog.Error("posters: generate failed", "error", err, "event_id", eventID)
		}
		_ = sw.Emit("error", map[string]any{"message": msg})
		return
	}
	_ = sw.Emit("done", map[string]any{
		"poster":   a.posterView(r, res.Poster),
		"options":  a.optionViews(r, res.Poster, res.BatchID),
		"skipped":  res.Skipped,
		"no_photo": res.NoPhoto,
	})
}
