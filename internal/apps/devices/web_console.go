package devices

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/console"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/web/qrcode"
)

// The approver's side, and the Devices page. Admin only (decided): pairing
// hands a machine a standing grant, and that is a workspace decision.

func registerConsoleRoutes(mux apps.Mux, a *App) {
	route := func(h http.HandlerFunc) http.Handler {
		return console.AdminJSON(a.pool, a.signer, h)
	}
	mux.Handle("GET /{slug}/api/devices", route(a.handleList))
	mux.Handle("GET /{slug}/api/devices/pair.svg", route(a.handlePairQR))
	mux.Handle("POST /{slug}/api/devices/pairings/approve", route(a.handleApprove))
	mux.Handle("POST /{slug}/api/devices/pairings/{id}/cancel", route(a.handleCancel))
	mux.Handle("PATCH /{slug}/api/devices/{id}", route(a.handleUpdate))
	mux.Handle("DELETE /{slug}/api/devices/{id}", route(a.handleRevoke))
}

type deviceJSON struct {
	ID           string     `json:"id"`
	Label        string     `json:"label"`
	Capabilities []string   `json:"capabilities"`
	CreatedAt    time.Time  `json:"created_at"`
	LastSeenAt   *time.Time `json:"last_seen_at"`
	RevokedAt    *time.Time `json:"revoked_at"`
	// HasStartLink: a kiosk start link exists (the link itself is shown
	// once, when made).
	HasStartLink bool `json:"has_start_link"`
}

func deviceToJSON(a models.Actor) deviceJSON {
	caps := a.Capabilities
	if caps == nil {
		caps = []string{}
	}
	return deviceJSON{
		ID: a.ID.String(), Label: a.Label, Capabilities: caps,
		CreatedAt: a.CreatedAt, LastSeenAt: a.LastSeenAt, RevokedAt: a.RevokedAt,
		HasStartLink: a.HasStartToken,
	}
}

type pendingJSON struct {
	ID        string    `json:"id"`
	Pictures  []string  `json:"pictures"`
	ExpiresAt time.Time `json:"expires_at"`
}

// handleList returns everything the Devices page needs in one read.
func (a *App) handleList(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	actors, err := models.ListActors(r.Context(), a.pool, tenant.ID, models.ActorKindDevice)
	if err != nil {
		serverError(w, "listing devices", err)
		return
	}
	pending, err := pendingPairings(r.Context(), a.pool, tenant.ID)
	if err != nil {
		serverError(w, "listing pairings", err)
		return
	}
	devices := make([]deviceJSON, 0, len(actors))
	for _, act := range actors {
		devices = append(devices, deviceToJSON(act))
	}
	waiting := make([]pendingJSON, 0, len(pending))
	for i := range pending {
		waiting = append(waiting, pendingJSON{ID: pending[i].ID.String(), Pictures: pending[i].choices(), ExpiresAt: pending[i].ExpiresAt})
	}
	writeJSON(w, map[string]any{
		"devices":      devices,
		"pending":      waiting,
		"presets":      presets,
		"capabilities": capabilityInfos(),
		"pair_url":     a.pairURL(tenant.Slug),
	})
}

// pairURL is the address a device opens, absolute so it can be read out,
// copied into a message, or encoded in the QR.
func (a *App) pairURL(slug string) string {
	return a.baseURL + "/" + slug + "/pair"
}

// handlePairQR serves the pairing address as a QR, so a phone or an iPad
// pairs by pointing its camera at the admin's screen instead of typing.
// The admin's screen, not the device's: the code is the address, not the
// credential, so showing it is harmless.
func (a *App) handlePairQR(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	svg, err := qrcode.RenderSVG(a.pairURL(tenant.Slug), 320, "Pairing address")
	if err != nil {
		serverError(w, "rendering pairing qr", err)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	if _, err := w.Write([]byte(svg)); err != nil {
		slog.Warn("devices: writing pairing qr", "error", err)
	}
}

// approveRequest identifies the pairing by a tapped picture or a typed
// code, and says what the device becomes.
type approveRequest struct {
	PairingID    string   `json:"pairing_id"`
	Picture      string   `json:"picture"`
	Code         string   `json:"code"`
	Label        string   `json:"label"`
	Capabilities []string `json:"capabilities"`
}

func (a *App) handleApprove(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	caller := auth.CallerFromContext(r.Context())
	var req approveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		clientError(w, r, http.StatusBadRequest, "invalid JSON")
		return
	}
	label := strings.TrimSpace(req.Label)
	if label == "" || len(label) > 80 {
		clientError(w, r, http.StatusBadRequest, "a device needs a label")
		return
	}
	caps, ok := normaliseCapabilities(req.Capabilities)
	if !ok || len(caps) == 0 {
		clientError(w, r, http.StatusBadRequest, "pick at least one capability the device may use")
		return
	}
	pairing, status, msg := a.matchPairing(r, tenant.ID, req)
	if pairing == nil {
		clientError(w, r, status, msg)
		return
	}
	actor, err := models.CreateActor(r.Context(), a.pool, tenant.ID, models.ActorKindDevice, label, caps, sponsorOf(caller))
	if err != nil {
		serverError(w, "creating device actor", err)
		return
	}
	if err := setPairingStatus(r.Context(), a.pool, tenant.ID, pairing.ID, statusApproved, &actor.ID); err != nil {
		if errors.Is(err, errGone) {
			clientError(w, r, http.StatusGone, "that pairing is no longer waiting")
			return
		}
		serverError(w, "approving pairing", err)
		return
	}
	slog.Info("devices: pairing approved", "tenant_id", tenant.ID, "actor_id", actor.ID, "label", label, "by", caller.UserID)
	writeJSON(w, map[string]any{"device": deviceToJSON(*actor)})
}

// matchPairing resolves the request to one pending pairing. A wrong
// picture cancels that pairing: the admin was looking at a different
// screen, and whatever is on this one should not get a second guess.
func (a *App) matchPairing(r *http.Request, tenantID uuid.UUID, req approveRequest) (*Pairing, int, string) {
	code := strings.ToUpper(strings.TrimSpace(req.Code))
	if code != "" {
		p, err := pendingPairing(r.Context(), a.pool, tenantID, nil, code)
		if err != nil || p == nil {
			return nil, http.StatusNotFound, "no device is waiting with that code"
		}
		return p, 0, ""
	}
	id, err := uuid.Parse(req.PairingID)
	if err != nil {
		return nil, http.StatusBadRequest, "tap a picture or type the code"
	}
	p, err := pendingPairing(r.Context(), a.pool, tenantID, &id, "")
	if err != nil || p == nil {
		return nil, http.StatusGone, "that pairing is no longer waiting"
	}
	if req.Picture != p.Picture {
		if err := setPairingStatus(r.Context(), a.pool, tenantID, p.ID, statusCancelled, nil); err != nil {
			slog.Warn("devices: cancelling pairing after wrong picture", "error", err)
		}
		return nil, http.StatusConflict, "that isn't the picture on the device; the pairing was cancelled"
	}
	return p, 0, ""
}

func sponsorOf(c *services.Caller) *uuid.UUID {
	if c == nil || !c.IsUser() {
		return nil
	}
	id := c.UserID
	return &id
}

func (a *App) handleCancel(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := setPairingStatus(r.Context(), a.pool, tenant.ID, id, statusCancelled, nil); err != nil && !errors.Is(err, errGone) {
		serverError(w, "cancelling pairing", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type updateRequest struct {
	Label        string   `json:"label"`
	Capabilities []string `json:"capabilities"`
}

func (a *App) handleUpdate(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var req updateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		clientError(w, r, http.StatusBadRequest, "invalid JSON")
		return
	}
	label := strings.TrimSpace(req.Label)
	if label == "" || len(label) > 80 {
		clientError(w, r, http.StatusBadRequest, "a device needs a label")
		return
	}
	caps, ok := normaliseCapabilities(req.Capabilities)
	if !ok {
		clientError(w, r, http.StatusBadRequest, "unknown capability")
		return
	}
	if err := models.UpdateActor(r.Context(), a.pool, tenant.ID, id, label, caps); err != nil {
		if errors.Is(err, models.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		serverError(w, "updating device", err)
		return
	}
	actor, err := models.GetActor(r.Context(), a.pool, tenant.ID, id)
	if err != nil || actor == nil {
		serverError(w, "reloading device", err)
		return
	}
	writeJSON(w, map[string]any{"device": deviceToJSON(*actor)})
}

func (a *App) handleRevoke(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := models.RevokeActor(r.Context(), a.pool, tenant.ID, id); err != nil {
		if errors.Is(err, models.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		serverError(w, "revoking device", err)
		return
	}
	slog.Info("devices: revoked", "tenant_id", tenant.ID, "actor_id", id)
	w.WriteHeader(http.StatusNoContent)
}

func serverError(w http.ResponseWriter, what string, err error) {
	slog.Error("devices: "+what, "error", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

// clientError answers a 4xx and logs it, so a refusal is debuggable from
// the server side.
func clientError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	slog.Warn("devices: request refused", "status", status, "method", r.Method, "path", r.URL.Path, "reason", msg)
	http.Error(w, msg, status)
}
