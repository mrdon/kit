package devices

import (
	"context"
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
	"github.com/mrdon/kit/internal/auth/opaquetoken"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/web/qrcode"
)

// Start links, for browsers that cannot keep a cookie.
//
// Pairing hands the device a session cookie, and that is the right shape
// for a phone or an iPad. A kiosk-mode browser is different: it boots to
// one fixed URL and starts every session with an empty cookie jar, so by
// morning the pairing is gone and the laptop is a stranger again. The
// start link is that fixed URL with a secret in it. Opened, it signs the
// browser in as this one device for the session and goes to the device
// home. The link IS the credential, so it is shown once when made,
// invalidated by making another, and ended by unpairing the device; what
// it grants is only the device's capabilities.

// startTokenPrefix marks a start-link secret in logs and pastes.
const startTokenPrefix = "kitd_"

func registerStartRoutes(mux apps.Mux, a *App) {
	tenantMW := auth.TenantFromPath(a.pool)
	mux.Handle("GET /{slug}/device/{token}", tenantMW(http.HandlerFunc(a.handleStart)))
	mux.Handle("POST /{slug}/api/devices/{id}/start-link",
		console.AdminJSON(a.pool, a.signer, a.handleMakeStartLink))
	// A kiosk never pairs: it is created here, by name, and gets its link
	// in the same breath.
	mux.Handle("POST /{slug}/api/devices",
		console.AdminJSON(a.pool, a.signer, a.handleCreateKiosk))
}

// createKioskRequest names a device that will only ever start from a link.
type createKioskRequest struct {
	Label        string   `json:"label"`
	Capabilities []string `json:"capabilities"`
}

// handleCreateKiosk creates a device without pairing and answers with its
// start link, the one time the plaintext is shown.
func (a *App) handleCreateKiosk(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	caller := auth.CallerFromContext(r.Context())
	var req createKioskRequest
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
	actor, err := models.CreateActor(r.Context(), a.pool, tenant.ID, models.ActorKindDevice, label, caps, sponsorOf(caller))
	if err != nil {
		serverError(w, "creating kiosk device", err)
		return
	}
	link, err := a.mintStartLink(r.Context(), tenant, actor.ID)
	if err != nil {
		serverError(w, "minting start link", err)
		return
	}
	actor.HasStartToken = true
	slog.Info("devices: kiosk created", "tenant_id", tenant.ID, "actor_id", actor.ID, "label", label, "by", caller.UserID)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{"device": deviceToJSON(*actor), "start_url": link.url, "qr_svg": link.qr})
}

type startLink struct {
	url string
	qr  string
}

// mintStartLink stores a fresh start token for the actor and returns the
// link with a QR of it, for a tablet that would rather scan than type.
func (a *App) mintStartLink(ctx context.Context, tenant *models.Tenant, actorID uuid.UUID) (startLink, error) {
	token, hash, err := opaquetoken.New(startTokenPrefix)
	if err != nil {
		return startLink{}, err
	}
	if err := models.SetActorStartToken(ctx, a.pool, tenant.ID, actorID, hash); err != nil {
		return startLink{}, err
	}
	url := a.baseURL + "/" + tenant.Slug + "/device/" + token
	svg, err := qrcode.RenderSVG(url, 320, "Device start link")
	if err != nil {
		return startLink{}, err
	}
	return startLink{url: url, qr: string(svg)}, nil
}

// handleStart signs the browser in as the device the link names.
func (a *App) handleStart(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.NotFound(w, r)
		return
	}
	// The same per-IP budget as pairing: a kiosk boots once a day, and a
	// stranger guessing links gets ten tries an hour, which against 256
	// random bits is none.
	if !a.limiter.allow(auth.ClientIP(r), time.Now()) {
		http.Error(w, "too many attempts; try again later", http.StatusTooManyRequests)
		return
	}
	actor, err := models.GetActorByStartToken(r.Context(), a.pool, tenant.ID, opaquetoken.Hash(r.PathValue("token")))
	if err != nil {
		slog.Error("devices: looking up start link", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if actor == nil {
		slog.Warn("devices: unknown start link", "tenant_id", tenant.ID, "ip", auth.ClientIP(r))
		http.Error(w, "this start link is not valid any more; make a new one on the Devices page", http.StatusNotFound)
		return
	}
	if err := a.signer.IssueDevice(r.Context(), w, a.pool, tenant.ID, actor.ID, actor.Label, "/"+tenant.Slug+"/"); err != nil {
		slog.Error("devices: issuing session from start link", "actor_id", actor.ID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	slog.Info("devices: started from link", "tenant_id", tenant.ID, "actor_id", actor.ID, "label", actor.Label)
	http.Redirect(w, r, "/"+tenant.Slug+"/"+console.Segment+"/", http.StatusSeeOther)
}

// handleMakeStartLink mints a device's start link, replacing any previous
// one. The plaintext goes back exactly once.
func (a *App) handleMakeStartLink(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	link, err := a.mintStartLink(r.Context(), tenant, id)
	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		serverError(w, "minting start link", err)
		return
	}
	caller := auth.CallerFromContext(r.Context())
	slog.Info("devices: start link made", "tenant_id", tenant.ID, "actor_id", id, "by", caller.UserID)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, map[string]any{"start_url": link.url, "qr_svg": link.qr})
}
