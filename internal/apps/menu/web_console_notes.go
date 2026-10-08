package menu

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/console"
	"github.com/mrdon/kit/internal/auth"
)

// Beer descriptions, one at a time.
//
// The printed menu's descriptions mostly have to be typed: Untappd's board
// carries none, and its consumer pages refuse Kit's server. The admin print
// page edits them inside the whole print config, which is setup. This is
// the narrow version for the bar iPad: the synced beers, each with its
// description and where it came from, and a save per beer. Same storage as
// the agent's set_menu_notes (config.notes, which wins over anything
// scraped), so a description written at the bar prints and survives a sync.
//
// Admin for people, the same as the print config it is a slice of.

func registerNotesRoutes(mux apps.Mux, a *App) {
	route := func(h http.HandlerFunc) http.Handler {
		return console.RequireCapAdmin(a.pool, a.signer, auth.CapMenuPrint, h)
	}
	mux.Handle("GET /{slug}/api/menu/print/notes", route(a.handleGetNotes))
	mux.Handle("PUT /{slug}/api/menu/print/notes", route(a.handleSaveNote))
}

func (a *App) handleGetNotes(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	a.writeNotes(w, r, tenant.ID)
}

// saveNoteRequest is one beer's description. Empty text removes a written
// description, letting a scraped one show through again.
type saveNoteRequest struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

func (a *App) handleSaveNote(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	var req saveNoteRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not read the request"})
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "which beer?"})
		return
	}
	text := strings.TrimSpace(req.Text)
	var err error
	if text == "" {
		err = DeletePrintNotes(r.Context(), a.pool, tenant.ID, []string{name})
	} else {
		err = MergePrintNotes(r.Context(), a.pool, tenant.ID, map[string]string{name: text})
	}
	if err != nil {
		slog.Error("saving beer description", "tenant_id", tenant.ID, "beer", name, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	a.writeNotes(w, r, tenant.ID)
}

// writeNotes answers with the synced beers and their descriptions, the
// same rows the admin print page shows.
func (a *App) writeNotes(w http.ResponseWriter, r *http.Request, tenantID uuid.UUID) {
	state, err := LoadPrintState(r.Context(), a.pool, tenantID)
	if err != nil {
		slog.Error("loading print state for notes", "tenant_id", tenantID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	beers := printBeers(state)
	if beers == nil {
		beers = []printBeer{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"beers":     beers,
		"synced_at": state.SyncedAt,
	})
}
