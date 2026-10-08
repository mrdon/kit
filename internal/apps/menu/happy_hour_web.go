package menu

import (
	"context"
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

// The happy hour settings page.
//
// Admin-only, like the printed menu: it changes what the register charges.
// Save and Sync are separate buttons for the same reason the tools are
// separate: the wall follows a save at once, while Square changes only when
// somebody presses Sync, and the page shows the sync's whole log -- Square's
// own error text included -- so a token without catalog permission is
// diagnosed on the page rather than in the server logs.

// Admin for people, and a paired device holding menu.happy_hour (the bar
// iPad) -- that is the capability's human default, so nothing changes for
// anyone signed in.
func registerHappyHourRoutes(mux apps.Mux, a *App) {
	route := func(h http.HandlerFunc) http.Handler {
		return console.RequireCap(a.pool, a.signer, auth.CapMenuHappyHour, h)
	}
	mux.Handle("GET /{slug}/api/menu/happy-hour", route(a.handleGetHappyHour))
	mux.Handle("PUT /{slug}/api/menu/happy-hour", route(a.handleSaveHappyHour))
	mux.Handle("POST /{slug}/api/menu/happy-hour/sync", route(a.handleSyncHappyHour))
	mux.Handle("POST /{slug}/api/menu/happy-hour/now", route(a.handleHappyHourNow))
}

// happyHourPayload is the wire shape.
type happyHourPayload struct {
	Config     HappyHour `json:"config"`
	Configured bool      `json:"configured"`
	// OnNow is whether happy hour is on this minute, on the wall and in Square.
	OnNow bool `json:"on_now"`
	// Until is when it ends, e.g. "5pm" or "Tue 5pm"; empty when on and only
	// End now will end it, or when off.
	Until    string `json:"until"`
	Timezone string `json:"timezone"`
	// Taps are the beers on the board, offered as checkboxes so a name is
	// picked rather than typed.
	Taps []happyTap `json:"taps"`

	InSync   bool       `json:"in_sync"`
	SyncedAt *time.Time `json:"synced_at"`
	SyncOK   bool       `json:"sync_ok"`
	SyncLog  string     `json:"sync_log"`
}

type happyTap struct {
	Name  string `json:"name"`
	Price string `json:"price"`
	Size  string `json:"size"`
}

func (a *App) happyHourPayload(ctx context.Context, tenantID uuid.UUID, tz string) (*happyHourPayload, error) {
	state, err := LoadHappyHour(ctx, a.pool, tenantID)
	if err != nil {
		return nil, err
	}
	loc, now := locationOf(tz), timeNow()
	out := &happyHourPayload{
		Config:     state.Config,
		Configured: state.Configured,
		OnNow:      state.OnAt(now, loc),
		Timezone:   tz,
		Taps:       []happyTap{},
		InSync:     state.InSync(now, loc),
		SyncedAt:   state.SyncedAt,
		SyncOK:     state.SyncOK,
		SyncLog:    state.SyncLog,
	}
	if out.OnNow {
		out.Until = state.Config.Until(now, loc)
	}
	if out.Config.Beers == nil {
		out.Config.Beers = []string{}
	}
	if row, err := GetBoard(ctx, a.pool, tenantID); err == nil && row != nil {
		if board, perr := ParseBoard(row.Payload); perr == nil {
			for _, t := range board.Taps {
				size := t.Size
				if size == "" {
					size = DefaultPour
				}
				out.Taps = append(out.Taps, happyTap{Name: t.Name, Price: t.Price, Size: size})
			}
		}
	}
	return out, nil
}

func (a *App) handleGetHappyHour(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	out, err := a.happyHourPayload(r.Context(), tenant.ID, tenant.Timezone)
	if err != nil {
		slog.Error("loading happy hour", "tenant_id", tenant.ID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *App) handleSaveHappyHour(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	var h HappyHour
	if err := json.NewDecoder(r.Body).Decode(&h); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not read the happy hour: " + err.Error()})
		return
	}
	if _, err := storeHappyHour(r.Context(), a.pool, tenant.ID, h); err != nil {
		if errors.Is(err, ErrPayloadInvalid) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		slog.Error("saving happy hour", "tenant_id", tenant.ID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	a.handleGetHappyHour(w, r)
}

// handleSyncHappyHour runs a Square sync, or a preview of one, and returns the
// log with the refreshed state. A failed sync is still a 200: the request
// worked, and what Square said is the content the page exists to show.
func (a *App) handleSyncHappyHour(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	var body struct {
		Apply bool `json:"apply"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not read the request"})
		return
	}
	log, ok := a.SyncHappyHour(r.Context(), tenant.ID, body.Apply)
	state, err := a.happyHourPayload(r.Context(), tenant.ID, tenant.Timezone)
	if err != nil {
		slog.Error("loading happy hour after sync", "tenant_id", tenant.ID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"state": state, "log": log, "ok": ok, "applied": body.Apply})
}

// handleHappyHourNow is Start now / End now. Square is pushed in the same
// request, and its log comes back, so whoever pressed it sees at once whether
// the register will ring it.
func (a *App) handleHappyHourNow(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	var body struct {
		On bool `json:"on"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "could not read the request"})
		return
	}
	log, ok, err := a.setHappyLive(r.Context(), tenant.ID, body.On)
	if err != nil {
		if errors.Is(err, ErrPayloadInvalid) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		slog.Error("setting happy hour now", "tenant_id", tenant.ID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	state, err := a.happyHourPayload(r.Context(), tenant.ID, tenant.Timezone)
	if err != nil {
		slog.Error("loading happy hour", "tenant_id", tenant.ID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"state": state, "log": log, "ok": ok, "applied": true})
}
