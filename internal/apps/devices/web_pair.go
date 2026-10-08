package devices

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/console"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/auth/opaquetoken"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
)

// The device's side of pairing. Both routes are unauthenticated by
// definition -- nobody is signed in on the device, ever -- so the pairing
// page is rate limited and the poll trusts only the device-code cookie.

// pairCookieName holds the device code between the page and its polls.
const pairCookieName = "kit_pair"

func registerPairRoutes(mux apps.Mux, a *App) {
	tenantMW := auth.TenantFromPath(a.pool)
	mux.Handle("GET /{slug}/pair", tenantMW(http.HandlerFunc(a.handlePairPage)))
	mux.Handle("GET /{slug}/pair/status", tenantMW(http.HandlerFunc(a.handlePairStatus)))
}

// handlePairPage shows the picture and code. A reload while the pairing is
// still pending shows the SAME ones, so an admin halfway through tapping
// isn't chasing a moving target.
//
// A browser that is already paired is sent to the device home instead.
// A kiosk boots to one fixed URL, and if that URL is this page it must
// not pair the machine again every morning; it should only pair when
// there is nothing to resume.
func (a *App) handlePairPage(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.NotFound(w, r)
		return
	}
	if caller, err := a.signer.CallerFromRequest(r.Context(), a.pool, r); err == nil && caller != nil && caller.Kind == services.CallerDevice && caller.TenantID == tenant.ID {
		http.Redirect(w, r, "/"+tenant.Slug+"/"+console.Segment+"/", http.StatusSeeOther)
		return
	}
	if err := sweepPairings(r.Context(), a.pool, tenant.ID); err != nil {
		slog.Warn("devices: sweeping pairings", "error", err)
	}
	pairing := a.existingPairing(r, tenant)
	if pairing == nil {
		var err error
		pairing, err = a.startPairing(w, r, tenant)
		if err != nil {
			return
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := pageTmpl.ExecuteTemplate(w, "pair.html", map[string]any{
		"WorkspaceName": tenantName(tenant),
		"Picture":       pairing.Picture,
		"UserCode":      pairing.UserCode,
		"PairURL":       "/" + tenant.Slug + "/pair",
		"StatusURL":     "/" + tenant.Slug + "/pair/status",
	}); err != nil {
		slog.Error("devices: rendering pair page", "error", err)
	}
}

// existingPairing returns the still-pending pairing behind the request's
// cookie, or nil.
func (a *App) existingPairing(r *http.Request, tenant *models.Tenant) *Pairing {
	c, err := r.Cookie(pairCookieName)
	if err != nil || c.Value == "" {
		return nil
	}
	p, err := pairingByDeviceCode(r.Context(), a.pool, tenant.ID, opaquetoken.Hash(c.Value))
	if err != nil || p == nil || p.Status != statusPending || time.Now().After(p.ExpiresAt) {
		return nil
	}
	return p
}

// startPairing mints a pending row and the cookie that owns it. On refusal
// it has already written the response and returns an error.
func (a *App) startPairing(w http.ResponseWriter, r *http.Request, tenant *models.Tenant) (*Pairing, error) {
	ip := auth.ClientIP(r)
	if !a.limiter.allow(ip, time.Now()) {
		slog.Warn("devices: pairing rate limited", "ip", ip)
		http.Error(w, "too many pairing attempts; try again later", http.StatusTooManyRequests)
		return nil, errors.New("rate limited")
	}
	n, err := countPending(r.Context(), a.pool, tenant.ID)
	if err != nil {
		slog.Error("devices: counting pairings", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, err
	}
	if n >= maxPendingPerTenant {
		http.Error(w, "too many devices are waiting to pair; try again in a few minutes", http.StatusTooManyRequests)
		return nil, errors.New("tenant pairing cap")
	}
	code, hash, err := opaquetoken.New("")
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, err
	}
	p, err := createPairing(r.Context(), a.pool, tenant.ID, hash, ip)
	if err != nil {
		slog.Error("devices: creating pairing", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     pairCookieName,
		Value:    code,
		Path:     "/" + tenant.Slug + "/pair",
		MaxAge:   int(pairingTTL.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	return p, nil
}

// handlePairStatus is the poll. Approved pairings are claimed here, once,
// and turned into a device session for the browser holding the cookie.
func (a *App) handlePairStatus(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	c, err := r.Cookie(pairCookieName)
	if err != nil || c.Value == "" {
		writeJSON(w, map[string]string{"status": "gone", "message": "This browser has no pairing in progress."})
		return
	}
	hash := opaquetoken.Hash(c.Value)
	p, err := pairingByDeviceCode(r.Context(), a.pool, tenant.ID, hash)
	if err != nil {
		slog.Error("devices: looking up pairing", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	switch {
	case p == nil || time.Now().After(p.ExpiresAt):
		writeJSON(w, map[string]string{"status": "gone", "message": "This pairing has expired."})
	case p.Status == statusCancelled:
		writeJSON(w, map[string]string{"status": "gone", "message": "The pairing was declined."})
	case p.Status == statusPending:
		writeJSON(w, map[string]string{"status": "pending"})
	default:
		a.completePairing(w, r, tenant, hash)
	}
}

// completePairing issues the device session and clears the pairing cookie.
func (a *App) completePairing(w http.ResponseWriter, r *http.Request, tenant *models.Tenant, hash string) {
	actorID, err := claimPairing(r.Context(), a.pool, tenant.ID, hash)
	if errors.Is(err, errGone) {
		writeJSON(w, map[string]string{"status": "gone", "message": "This pairing was already used."})
		return
	}
	if err != nil {
		slog.Error("devices: claiming pairing", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	actor, err := models.GetActor(r.Context(), a.pool, tenant.ID, actorID)
	if err != nil || actor == nil {
		slog.Error("devices: loading paired actor", "actor_id", actorID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := a.signer.IssueDevice(r.Context(), w, a.pool, tenant.ID, actor.ID, actor.Label, "/"+tenant.Slug+"/"); err != nil {
		slog.Error("devices: issuing device session", "actor_id", actorID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: pairCookieName, Value: "", Path: "/" + tenant.Slug + "/pair", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
	slog.Info("devices: paired", "tenant_id", tenant.ID, "actor_id", actor.ID, "label", actor.Label)
	writeJSON(w, map[string]string{
		"status":   "approved",
		"redirect": "/" + tenant.Slug + "/" + console.Segment + "/",
	})
}

func tenantName(t *models.Tenant) string {
	if t.Name != "" {
		return t.Name
	}
	return t.Slug
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("devices: writing json", "error", err)
	}
}
