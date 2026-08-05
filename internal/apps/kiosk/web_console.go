package kiosk

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/console"
	"github.com/mrdon/kit/internal/auth"
)

// registerConsoleRoutes wires the admin JSON API behind /{slug}/web/admin/kiosk.
// Admin-only: repointing a screen changes what a room full of people see.
func registerConsoleRoutes(mux apps.Mux, a *App) {
	adminJSON := func(h http.HandlerFunc) http.Handler {
		return console.AdminJSON(a.pool, a.signer, h)
	}
	mux.Handle("GET /{slug}/api/kiosk/boards", adminJSON(a.handleList))
	mux.Handle("POST /{slug}/api/kiosk/boards", adminJSON(a.handleCreate))
	mux.Handle("PATCH /{slug}/api/kiosk/boards/{id}", adminJSON(a.handleUpdate))
	mux.Handle("DELETE /{slug}/api/kiosk/boards/{id}", adminJSON(a.handleDelete))
}

// boardJSON is the wire shape. public_url is served rather than assembled
// client-side so the console and the redirect handler can never disagree
// about where a board actually lives.
type boardJSON struct {
	ID         string     `json:"id"`
	Key        string     `json:"key"`
	Name       string     `json:"name"`
	URL        string     `json:"url"`
	Notes      string     `json:"notes"`
	PublicURL  string     `json:"public_url"`
	LastSeenAt *time.Time `json:"last_seen_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

type boardInputJSON struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	URL   string `json:"url"`
	Notes string `json:"notes"`
}

func (a *App) toJSON(b *Board, slug string) boardJSON {
	return boardJSON{
		ID:         b.ID.String(),
		Key:        b.Key,
		Name:       b.Name,
		URL:        b.URL,
		Notes:      b.Notes,
		PublicURL:  a.baseURL + PublicPath(slug, b.Key),
		LastSeenAt: b.LastSeenAt,
		UpdatedAt:  b.UpdatedAt,
	}
}

func (a *App) handleList(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	boards, err := a.svc.List(r.Context(), tenant.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	out := make([]boardJSON, 0, len(boards))
	for _, b := range boards {
		out = append(out, a.toJSON(b, tenant.Slug))
	}
	writeJSON(w, http.StatusOK, map[string]any{"boards": out})
}

func (a *App) handleCreate(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	in, ok := decodeInput(w, r)
	if !ok {
		return
	}
	b, err := a.svc.Create(r.Context(), tenant.ID, in)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a.toJSON(b, tenant.Slug))
}

func (a *App) handleUpdate(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid board id"})
		return
	}
	in, ok := decodeInput(w, r)
	if !ok {
		return
	}
	b, err := a.svc.Update(r.Context(), tenant.ID, id, in)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a.toJSON(b, tenant.Slug))
}

func (a *App) handleDelete(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid board id"})
		return
	}
	if err := a.svc.Delete(r.Context(), tenant.ID, id); err != nil {
		writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeInput(w http.ResponseWriter, r *http.Request) (BoardInput, bool) {
	var body boardInputJSON
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return BoardInput{}, false
	}
	return BoardInput(body), true
}

// writeErr maps the service's sentinels onto status codes; anything else is a
// 500 with the detail logged rather than returned.
func writeErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrKeyTaken):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrKeyInvalid), errors.Is(err, ErrNameRequired), errors.Is(err, ErrURLInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		slog.Error("kiosk console request failed", "path", r.URL.Path, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Warn("encoding kiosk response", "error", err)
	}
}
