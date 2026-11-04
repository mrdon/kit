package cogs

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/console"
	"github.com/mrdon/kit/internal/auth"
)

// registerBrewerRoutes is the API behind the brewer's screens. It is
// capability-gated: the paired brewhouse iPad holds cogs.log, and any
// member passes the same check, so the console's own COGS pages use
// these routes too. What only an admin may do (a day-one load, reversing
// an old entry) is decided inside the service from the caller.
func registerBrewerRoutes(mux apps.Mux, a *App) {
	route := func(h http.HandlerFunc) http.Handler {
		return console.RequireCap(a.pool, a.signer, auth.CapCogsLog, h)
	}
	mux.Handle("GET /{slug}/api/cogs/state", route(a.handleState))
	mux.Handle("GET /{slug}/api/cogs/history", route(a.handleHistory))
	mux.Handle("POST /{slug}/api/cogs/actions", route(a.handleAction))
	mux.Handle("POST /{slug}/api/cogs/actions/{id}/undo", route(a.handleUndo))
	mux.Handle("POST /{slug}/api/cogs/plan/{id}/flag", route(a.handleFlagPlan))
	mux.Handle("POST /{slug}/api/cogs/photos", route(a.handlePhotoUpload))
	mux.Handle("GET /{slug}/api/cogs/photos/{id}",
		console.RequireCapPage(a.pool, a.signer, auth.CapCogsLog, a.handlePhoto))
}

func (a *App) handleState(w http.ResponseWriter, r *http.Request) {
	st, err := a.svc.State(r.Context(), auth.CallerFromContext(r.Context()))
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (a *App) handleHistory(w http.ResponseWriter, r *http.Request) {
	var vessel *uuid.UUID
	if v := r.URL.Query().Get("vessel_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid vessel id"})
			return
		}
		vessel = &id
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	entries, err := a.svc.History(r.Context(), auth.CallerFromContext(r.Context()), vessel, limit)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (a *App) handleAction(w http.ResponseWriter, r *http.Request) {
	var in ActionInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	res, err := a.svc.Record(r.Context(), auth.CallerFromContext(r.Context()), in)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *App) handleUndo(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	res, err := a.svc.Undo(r.Context(), auth.CallerFromContext(r.Context()), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *App) handleFlagPlan(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Note string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if err := a.svc.FlagPlan(r.Context(), auth.CallerFromContext(r.Context()), id, body.Note); err != nil {
		writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handlePhotoUpload takes the raw image as the body, its type in
// Content-Type.
func (a *App) handlePhotoUpload(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(io.LimitReader(r.Body, maxPhotoBytes+1))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not read the photo"})
		return
	}
	tenant := auth.TenantFromContext(r.Context())
	id, err := a.svc.SavePhoto(r.Context(), tenant.ID, r.Header.Get("Content-Type"), data)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id.String()})
}

func (a *App) handlePhoto(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	tenant := auth.TenantFromContext(r.Context())
	mime, data, err := a.svc.Photo(r.Context(), tenant.ID, id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	if _, err := w.Write(data); err != nil {
		slog.Warn("writing brewing photo", "error", err)
	}
}

func pathID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return uuid.Nil, false
	}
	return id, true
}

// writeErr maps the service's sentinels onto status codes. The messages
// are written for the brewer, so they go back as they are.
func writeErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrForbidden):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": userMessage(err)})
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrOverdraw), errors.Is(err, ErrBlend),
		errors.Is(err, ErrNotUndoable), errors.Is(err, ErrConflict):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": userMessage(err)})
	default:
		slog.Error("brewing request failed", "path", r.URL.Path, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Warn("encoding brewing response", "error", err)
	}
}
