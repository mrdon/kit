package menu

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/console"
	"github.com/mrdon/kit/internal/auth"
)

// registerConsoleRoutes wires the read-only JSON API behind /{slug}/web/menu.
//
// Read-only on purpose. Boards are authored elsewhere and pushed with
// set_menu_board; the console's job here is to answer the one question you
// cannot answer from the authoring side — "what URL do I paste into the
// screen?" Adding edit endpoints before there is an editing surface would be
// building the half of a feature nobody has asked for.
//
// Any member, not admin-only, matching the kiosk page: whoever is setting up
// a screen today needs the URL, and making them wait on an admin to read a
// link is how a board ends up stale.
func registerConsoleRoutes(mux apps.Mux, a *App) {
	mux.Handle("GET /{slug}/api/menu/boards",
		console.JSON(a.pool, a.signer, a.handleList))
}

// boardJSON is the wire shape. public_url is served rather than assembled
// client-side so the console and the render handler can never disagree about
// where a board actually lives.
type boardJSON struct {
	Key       string     `json:"key"`
	Name      string     `json:"name"`
	PublicURL string     `json:"public_url"`
	UpdatedAt *time.Time `json:"updated_at"`
	Taps      int        `json:"taps"`
	Panels    int        `json:"panels"`
	// Empty marks the workspace menu before anyone has set a tap list. The
	// address still works and still gets shown — the screen renders a
	// placeholder — so the page says "nothing set yet" rather than hiding it.
	Empty bool `json:"empty"`
	// Error is set when a stored board no longer parses, so the page can say
	// so rather than showing a link to a screen that will 500.
	Error string `json:"error,omitempty"`
}

func (a *App) handleList(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	rows, err := a.svc.List(r.Context(), tenant.ID)
	if err != nil {
		slog.Error("listing menu boards", "tenant_id", tenant.ID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	out := make([]boardJSON, 0, len(rows)+1)
	seenDefault := false
	for _, row := range rows {
		updated := row.UpdatedAt
		b := boardJSON{
			Key:       row.Key,
			Name:      row.Name,
			PublicURL: a.baseURL + PublicPath(tenant.Slug, row.Key),
			UpdatedAt: &updated,
		}
		if row.Key == DefaultKey {
			seenDefault = true
		}
		if board, err := ParseBoard(row.Payload); err != nil {
			b.Error = err.Error()
		} else {
			b.Taps, b.Panels = len(board.Taps), len(board.Panels)
		}
		out = append(out, b)
	}
	// The workspace menu is not something anyone creates, so it is listed
	// whether or not a row exists yet. Its address is the one thing this page
	// is for, and it works from the moment the app is enabled.
	if !seenDefault {
		out = append([]boardJSON{{
			Key:       DefaultKey,
			Name:      "Menu",
			PublicURL: a.baseURL + PublicPath(tenant.Slug, DefaultKey),
			Empty:     true,
		}}, out...)
	}
	writeJSON(w, http.StatusOK, map[string]any{"boards": out})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Warn("encoding menu response", "error", err)
	}
}
