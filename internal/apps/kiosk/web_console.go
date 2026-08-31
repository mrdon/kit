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

// registerConsoleRoutes wires the JSON API behind /{slug}/web/kiosk.
//
// Any member, not admin-only — same call the events app makes. Repointing a
// screen is operational work for whoever is running the room today, and
// making it wait on an admin is how a menu board ends up a week stale. The
// blast radius is bounded by the app itself: a board can only ever hold a URL
// somebody in the workspace chose.
func registerConsoleRoutes(mux apps.Mux, a *App) {
	jsonRoute := func(h http.HandlerFunc) http.Handler {
		return console.JSON(a.pool, a.signer, h)
	}
	mux.Handle("GET /{slug}/api/kiosk/boards", jsonRoute(a.handleList))
	mux.Handle("POST /{slug}/api/kiosk/boards", jsonRoute(a.handleCreate))
	mux.Handle("PATCH /{slug}/api/kiosk/boards/{id}", jsonRoute(a.handleUpdate))
	mux.Handle("DELETE /{slug}/api/kiosk/boards/{id}", jsonRoute(a.handleDelete))
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
	// RecentURLs is what this board pointed at before, newest first, so the
	// console can offer a one-click undo for a bad paste.
	RecentURLs []urlChangeJSON `json:"recent_urls"`
}

type urlChangeJSON struct {
	URL        string    `json:"url"`
	ReplacedAt time.Time `json:"replaced_at"`
}

func toHistoryJSON(changes []URLChange) []urlChangeJSON {
	out := make([]urlChangeJSON, 0, len(changes))
	for _, c := range changes {
		out = append(out, urlChangeJSON{URL: c.URL, ReplacedAt: c.ReplacedAt})
	}
	return out
}

type boardInputJSON struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	URL   string `json:"url"`
	Notes string `json:"notes"`
}

func (a *App) toJSON(b *Board, slug string, history []URLChange) boardJSON {
	return boardJSON{
		ID:         b.ID.String(),
		Key:        b.Key,
		Name:       b.Name,
		URL:        b.URL,
		Notes:      b.Notes,
		PublicURL:  a.baseURL + PublicPath(slug, b.Key),
		LastSeenAt: b.LastSeenAt,
		UpdatedAt:  b.UpdatedAt,
		RecentURLs: toHistoryJSON(history),
	}
}

func (a *App) handleList(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	boards, err := a.svc.List(r.Context(), tenant.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	history, err := ListURLHistoryByBoard(r.Context(), a.pool, tenant.ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	out := make([]boardJSON, 0, len(boards))
	for _, b := range boards {
		out = append(out, a.toJSON(b, tenant.Slug, history[b.ID]))
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
	writeJSON(w, http.StatusOK, a.toJSON(b, tenant.Slug, nil))
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
	history, err := a.svc.History(r.Context(), tenant.ID, id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a.toJSON(b, tenant.Slug, history))
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
