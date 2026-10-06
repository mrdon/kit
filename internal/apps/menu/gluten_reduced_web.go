package menu

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/console"
	"github.com/mrdon/kit/internal/auth"
)

// The gluten reduced checklist on the printed menu settings page.
//
// It is its own endpoint rather than a field of the print config because it is
// not print configuration: the wall reads it too, and it is stored as its own
// list. That also lets each checkbox save as it is ticked. The print form is
// one document saved whole, but a dietary mark that looks set and is not --
// ticked, and then the page closed before Save -- is the mistake worth
// designing out.

func registerGlutenReducedRoutes(mux apps.Mux, a *App) {
	adminRoute := func(h http.HandlerFunc) http.Handler {
		return console.AdminJSON(a.pool, a.signer, h)
	}
	mux.Handle("GET /{slug}/api/menu/gluten-reduced", adminRoute(a.handleGetGlutenReduced))
	mux.Handle("PUT /{slug}/api/menu/gluten-reduced", adminRoute(a.handleSaveGlutenReduced))
}

// glutenReducedPayload is the wire shape. Taps are the names on the board,
// offered as checkboxes so a name is picked rather than typed: matching is
// exact but for case and spacing, so a typed name is how a mark goes missing.
type glutenReducedPayload struct {
	Beers []string `json:"beers"`
	Taps  []string `json:"taps"`
}

func (a *App) glutenReducedPayload(ctx context.Context, tenantID uuid.UUID) (*glutenReducedPayload, error) {
	beers, err := LoadGlutenReduced(ctx, a.pool, tenantID)
	if err != nil {
		return nil, err
	}
	out := &glutenReducedPayload{Beers: beers, Taps: []string{}}
	if row, err := GetBoard(ctx, a.pool, tenantID); err == nil && row != nil {
		if board, perr := ParseBoard(row.Payload); perr == nil {
			for _, t := range board.Taps {
				out.Taps = append(out.Taps, t.Name)
			}
		}
	}
	return out, nil
}

func (a *App) handleGetGlutenReduced(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	out, err := a.glutenReducedPayload(r.Context(), tenant.ID)
	if err != nil {
		slog.Error("loading gluten reduced beers", "tenant_id", tenant.ID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *App) handleSaveGlutenReduced(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	var body struct {
		Beers []string `json:"beers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not read the list: " + err.Error()})
		return
	}
	if err := SaveGlutenReduced(r.Context(), a.pool, tenant.ID, body.Beers); err != nil {
		slog.Error("saving gluten reduced beers", "tenant_id", tenant.ID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	a.handleGetGlutenReduced(w, r)
}
