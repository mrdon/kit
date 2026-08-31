package menu

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/console"
	"github.com/mrdon/kit/internal/auth"
)

// registerConsoleRoutes wires the read-only JSON API behind /{slug}/web/menu.
//
// Read-only on purpose. The tap list follows Untappd and the presentation is
// pushed with set_menu_board; the console's job is to answer the one question
// neither of those can — "what URL do I paste into the screen?" Adding edit
// endpoints before there is an editing surface would be half a feature.
//
// Any member, not admin-only, matching the kiosk page: whoever is setting up a
// screen today needs the address, and making them wait on an admin to read a
// link is how a screen ends up showing the wrong thing.
func registerConsoleRoutes(mux apps.Mux, a *App) {
	mux.Handle("GET /{slug}/api/menu/board", console.JSON(a.pool, a.signer, a.handleShow))
}

// boardJSON is the wire shape. public_url is served rather than assembled
// client-side so the console and the render handler can never disagree about
// where the menu actually lives.
type boardJSON struct {
	Name      string     `json:"name"`
	PublicURL string     `json:"public_url"`
	UpdatedAt *time.Time `json:"updated_at"`
	Taps      int        `json:"taps"`
	Panels    int        `json:"panels"`
	// Empty marks a workspace with no tap list yet. The address still works —
	// the screen renders a placeholder — so the page shows it either way.
	Empty bool `json:"empty"`
	// Source is the Untappd board being followed, blank when set by hand.
	Source     string     `json:"source"`
	SyncedAt   *time.Time `json:"synced_at"`
	SyncError  string     `json:"sync_error,omitempty"`
	ParseError string     `json:"parse_error,omitempty"`
}

func (a *App) handleShow(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	out := boardJSON{
		Name:      "Menu",
		PublicURL: a.baseURL + PublicPath(tenant.Slug),
		Empty:     true,
	}

	row, err := a.svc.Get(r.Context(), tenant.ID)
	switch {
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusOK, out)
		return
	case err != nil:
		slog.Error("loading menu board", "tenant_id", tenant.ID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}

	out.Name = row.Name
	out.Empty = false
	out.UpdatedAt = &row.UpdatedAt
	out.Source = row.SourceID
	out.SyncedAt = row.SyncedAt
	out.SyncError = row.SyncError
	if board, perr := ParseBoard(row.Payload); perr != nil {
		out.ParseError = perr.Error()
	} else {
		out.Taps, out.Panels = len(board.Taps), len(board.Panels)
	}
	writeJSON(w, http.StatusOK, out)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Warn("encoding menu response", "error", err)
	}
}
