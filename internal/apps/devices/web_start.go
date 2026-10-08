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

// startCodeLen is how long the secret in a start link is. Six characters
// of the unambiguous alphabet is 4.8e8 codes, which the admin types into a
// kiosk's configuration by hand once; a longer one would be safer against
// nothing the rate limits don't already stop. A wrong guess costs one of
// ten tries per IP per ten minutes AND one of ten per workspace, so a
// brute force runs at 1440 guesses a day against half a billion.
const startCodeLen = 6

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
// start link, the one time the code is shown.
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
	writeJSON(w, map[string]any{"device": deviceToJSON(*actor), "start_url": link.url})
}

type startLink struct {
	url string
}

// mintStartLink stores a fresh start code for the actor and returns the
// link, short enough to type into a kiosk's configuration.
func (a *App) mintStartLink(ctx context.Context, tenant *models.Tenant, actorID uuid.UUID) (startLink, error) {
	code, err := randomCode(startCodeLen)
	if err != nil {
		return startLink{}, err
	}
	if err := models.SetActorStartToken(ctx, a.pool, tenant.ID, actorID, opaquetoken.Hash(code)); err != nil {
		return startLink{}, err
	}
	return startLink{url: a.baseURL + "/" + tenant.Slug + "/device/" + code}, nil
}

// handleStart signs the browser in as the device the link names.
func (a *App) handleStart(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.NotFound(w, r)
		return
	}
	// Two budgets, both spent only by failures: per IP, and per workspace
	// so a guesser rotating addresses gets no further.
	ip, tenantKey := auth.ClientIP(r), "tenant:"+tenant.ID.String()
	if !a.limiter.allow(ip, time.Now()) || !a.startFailures.allow(tenantKey, time.Now()) {
		http.Error(w, "too many attempts; try again later", http.StatusTooManyRequests)
		return
	}
	code := strings.ToUpper(strings.TrimSpace(r.PathValue("token")))
	actor, err := models.GetActorByStartToken(r.Context(), a.pool, tenant.ID, opaquetoken.Hash(code))
	if err != nil {
		slog.Error("devices: looking up start link", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if actor == nil {
		slog.Warn("devices: unknown start link", "tenant_id", tenant.ID, "ip", ip)
		http.Error(w, "this start link is not valid any more; make a new one on the Devices page", http.StatusNotFound)
		return
	}
	// A good link refunds its tries: a kiosk that boots five times in a row
	// is not an attacker.
	a.limiter.refund(ip)
	a.startFailures.refund(tenantKey)
	if err := a.signer.IssueDevice(r.Context(), w, a.pool, tenant.ID, actor.ID, actor.Label, "/"+tenant.Slug+"/"); err != nil {
		slog.Error("devices: issuing session from start link", "actor_id", actor.ID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	slog.Info("devices: started from link", "tenant_id", tenant.ID, "actor_id", actor.ID, "label", actor.Label)
	http.Redirect(w, r, "/"+tenant.Slug+"/"+console.Segment+"/", http.StatusSeeOther)
}

// handleMakeStartLink mints a device's start link, replacing any previous
// one. The code goes back exactly once.
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
	writeJSON(w, map[string]any{"start_url": link.url})
}
