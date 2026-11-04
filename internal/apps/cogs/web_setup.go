package cogs

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/console"
	"github.com/mrdon/kit/internal/auth"
)

// registerSetupRoutes is the console's admin API: the brewery's vessels,
// beers, formats and settings, the plan and the queue.
func registerSetupRoutes(mux apps.Mux, a *App) {
	admin := func(h http.HandlerFunc) http.Handler { return console.AdminJSON(a.pool, a.signer, h) }
	mux.Handle("GET /{slug}/api/cogs/setup", admin(a.handleSetup))
	mux.Handle("POST /{slug}/api/cogs/vessels", admin(a.handleSaveVessel))
	mux.Handle("PATCH /{slug}/api/cogs/vessels/{id}", admin(a.handleSaveVessel))
	mux.Handle("POST /{slug}/api/cogs/vessels/bulk", admin(a.handleBulkVessels))
	mux.Handle("POST /{slug}/api/cogs/beers", admin(a.handleSaveBeer))
	mux.Handle("PATCH /{slug}/api/cogs/beers/{id}", admin(a.handleSaveBeer))
	mux.Handle("POST /{slug}/api/cogs/beers/bulk", admin(a.handleBulkBeers))
	mux.Handle("POST /{slug}/api/cogs/formats", admin(a.handleSaveFormat))
	mux.Handle("PATCH /{slug}/api/cogs/formats/{id}", admin(a.handleSaveFormat))
	mux.Handle("PUT /{slug}/api/cogs/settings", admin(a.handleSaveSettings))
	mux.Handle("GET /{slug}/api/cogs/queue", admin(a.handleQueue))
	mux.Handle("POST /{slug}/api/cogs/queue/{id}/resolve", admin(a.handleResolveQueue))
	mux.Handle("POST /{slug}/api/cogs/plan", admin(a.handleSavePlan))
	mux.Handle("PATCH /{slug}/api/cogs/plan/{id}", admin(a.handleSavePlan))
	mux.Handle("DELETE /{slug}/api/cogs/plan/{id}", admin(a.handleCancelPlan))
}

// optionalID reads {id} when the route has one: PATCH edits, POST creates.
func optionalID(w http.ResponseWriter, r *http.Request) (*uuid.UUID, bool) {
	if r.PathValue("id") == "" {
		return nil, true
	}
	id, ok := pathID(w, r)
	if !ok {
		return nil, false
	}
	return &id, true
}

func decodeBody(w http.ResponseWriter, r *http.Request, into any) bool {
	if err := json.NewDecoder(r.Body).Decode(into); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return false
	}
	return true
}

func (a *App) handleSetup(w http.ResponseWriter, r *http.Request) {
	out, err := a.svc.Setup(r.Context(), auth.TenantFromContext(r.Context()).ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// saved answers a create or edit with the row's id.
func saved(w http.ResponseWriter, r *http.Request, id uuid.UUID, err error) {
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": id.String()})
}

func (a *App) handleSaveVessel(w http.ResponseWriter, r *http.Request) {
	id, ok := optionalID(w, r)
	var in VesselInput
	if !ok || !decodeBody(w, r, &in) {
		return
	}
	out, err := a.svc.SaveVessel(r.Context(), auth.TenantFromContext(r.Context()).ID, id, in)
	saved(w, r, out, err)
}

func (a *App) handleBulkVessels(w http.ResponseWriter, r *http.Request) {
	var in BulkVessels
	if !decodeBody(w, r, &in) {
		return
	}
	n, err := a.svc.AddVessels(r.Context(), auth.TenantFromContext(r.Context()).ID, in)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"added": n})
}

func (a *App) handleSaveBeer(w http.ResponseWriter, r *http.Request) {
	id, ok := optionalID(w, r)
	var in Beer
	if !ok || !decodeBody(w, r, &in) {
		return
	}
	out, err := a.svc.SaveBeer(r.Context(), auth.TenantFromContext(r.Context()).ID, id, in)
	saved(w, r, out, err)
}

func (a *App) handleBulkBeers(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Names []string `json:"names"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	n, err := a.svc.AddBeers(r.Context(), auth.TenantFromContext(r.Context()).ID, in.Names)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"added": n})
}

func (a *App) handleSaveFormat(w http.ResponseWriter, r *http.Request) {
	id, ok := optionalID(w, r)
	var in FormatInput
	if !ok || !decodeBody(w, r, &in) {
		return
	}
	out, err := a.svc.SaveFormat(r.Context(), auth.TenantFromContext(r.Context()).ID, id, in)
	saved(w, r, out, err)
}

func (a *App) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	var in Settings
	if !decodeBody(w, r, &in) {
		return
	}
	if err := a.svc.SaveSettings(r.Context(), auth.TenantFromContext(r.Context()).ID, in); err != nil {
		writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleQueue(w http.ResponseWriter, r *http.Request) {
	items, err := a.svc.OpenQueue(r.Context(), auth.TenantFromContext(r.Context()).ID)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *App) handleResolveQueue(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := a.svc.ResolveQueue(r.Context(), auth.TenantFromContext(r.Context()).ID, id); err != nil {
		writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) handleSavePlan(w http.ResponseWriter, r *http.Request) {
	id, ok := optionalID(w, r)
	var in PlanInput
	if !ok || !decodeBody(w, r, &in) {
		return
	}
	out, err := a.svc.SavePlan(r.Context(), auth.TenantFromContext(r.Context()).ID, id, in)
	saved(w, r, out, err)
}

func (a *App) handleCancelPlan(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := a.svc.CancelPlan(r.Context(), auth.TenantFromContext(r.Context()).ID, id); err != nil {
		writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// userMessage drops the sentinel's prefix ("invalid: ") from an error
// written for a person.
func userMessage(err error) string {
	msg := err.Error()
	for _, s := range []error{ErrInvalid, ErrOverdraw, ErrBlend, ErrNotUndoable, ErrForbidden, ErrConflict} {
		msg = strings.TrimPrefix(msg, s.Error()+": ")
	}
	if msg != "" {
		msg = strings.ToUpper(msg[:1]) + msg[1:]
	}
	return msg
}
