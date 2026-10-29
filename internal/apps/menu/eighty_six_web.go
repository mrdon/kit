package menu

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/console"
	"github.com/mrdon/kit/internal/auth"
)

// The 86 checklist on the Menu page and the bar's device.
//
// It reuses the gluten reduced wire shape -- the list, plus the board's names
// to tick -- and saves on every tick for the same reason: an 86 that looks set
// and was never saved is a guest ordering a beer that is gone.

// Any member, and a paired device holding menu.eighty_six.
func registerEightySixRoutes(mux apps.Mux, a *App) {
	route := func(h http.HandlerFunc) http.Handler {
		return console.RequireCap(a.pool, a.signer, auth.CapMenuEightySix, h)
	}
	mux.Handle("GET /{slug}/api/menu/86", route(a.handleGetEightySix))
	mux.Handle("PUT /{slug}/api/menu/86", route(a.handleSaveEightySix))
}

func (a *App) handleGetEightySix(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	beers, err := LoadEightySix(r.Context(), a.pool, tenant.ID)
	if err != nil {
		slog.Error("loading 86'd beers", "tenant_id", tenant.ID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	taps, err := a.tapNames(r.Context(), tenant.ID)
	if err != nil {
		taps = []string{}
	}
	writeJSON(w, http.StatusOK, glutenReducedPayload{Beers: beers, Taps: taps})
}

func (a *App) handleSaveEightySix(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	var body struct {
		Beers []string `json:"beers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not read the list: " + err.Error()})
		return
	}
	prev, err := LoadEightySix(r.Context(), a.pool, tenant.ID)
	if err != nil {
		slog.Error("loading 86'd beers", "tenant_id", tenant.ID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	caller := auth.CallerFromContext(r.Context())
	if err := a.saveEightySixBy(r.Context(), tenant.ID, caller, prev, body.Beers); err != nil {
		slog.Error("saving 86'd beers", "tenant_id", tenant.ID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	a.handleGetEightySix(w, r)
}
