package events

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/auth"
)

// One-time poster upload links.
//
// WHY THIS EXISTS. A tool call carries JSON, not bytes, so an agent -- the
// chat agent, or a harness driving Kit over MCP -- can author a whole event
// and still have no way to put the poster on it. The console form was the only
// door, which meant "created by the agent, poster added by hand later", and
// the poster is the part that actually gets seen. This is the door for
// callers that hold a tool, not a browser session: create_event hands back a
// URL, the caller POSTs the image to it, done.
//
// WHY A TOKEN AND NOT A SESSION. The point is to hand the address to something
// that cannot hold a cookie. So the URL itself is the credential, and every
// property below follows from that being true:
//
//   - Opaque, not signed. The token is 32 random bytes and means nothing on
//     its own; the grant it stands for -- tenant, user, event -- lives in
//     Redis under it. Nothing can be forged from the token, and nothing can be
//     learned from it, because it carries no claims to forge or read.
//   - Single use, atomically. Consumption is GETDEL, so two racing requests
//     cannot both win. This is the property a signed token could not give us
//     honestly: the deep-link signer's replay guard is an in-process map with
//     its own TTL, which forgets across a restart and across instances.
//   - Fifteen minutes. Long enough to mint a link and pipe a file at it, short
//     enough that a token leaked into a log or a shell history is dead before
//     anyone reads it.
//   - Narrow. The grant names one event. A token minted for one event cannot
//     touch another, cannot read anything, and cannot do anything but replace
//     one image.
//
// Redis is optional in Kit's config (it otherwise caches web_fetch), so
// everything here degrades to a clear "not configured" rather than minting
// links nothing can redeem.

// posterUploadTTL is how long a minted link stays redeemable. Also the Redis
// key's expiry, so an unused grant deletes itself rather than accumulating.
const posterUploadTTL = 15 * time.Minute

// posterUploadKeyPrefix namespaces the grant keys. Kit shares one Redis with
// the web_fetch cache.
const posterUploadKeyPrefix = "kit:events:poster-upload:"

// errUploadsUnconfigured is returned when Redis is absent. Surfaced to the
// caller verbatim: a tool that quietly returns no link reads as a bug, while
// this names the missing piece.
var errUploadsUnconfigured = errors.New("one-time poster upload links need Redis (set REDIS_URL)")

// posterGrant is what a token stands for. The event is bound at mint time, so
// redeeming a token cannot be redirected at a different event by changing the
// URL -- the path is checked against this, not trusted.
type posterGrant struct {
	TenantID uuid.UUID `json:"tenant_id"`
	UserID   uuid.UUID `json:"user_id"`
	EventID  uuid.UUID `json:"event_id"`
}

// mintPosterUpload stores a grant and returns the token. The caller must
// already have established that this user may edit this event.
func (a *App) mintPosterUpload(ctx context.Context, g posterGrant) (string, error) {
	if a.redis == nil {
		return "", errUploadsUnconfigured
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("poster upload token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw[:])
	body, err := json.Marshal(g)
	if err != nil {
		return "", fmt.Errorf("poster upload grant: %w", err)
	}
	// NX rather than a plain set: a token collision is not credible at 32
	// bytes, but silently overwriting someone else's live grant is the one
	// outcome worth refusing outright rather than making
	// impossible-by-assumption. A refused write comes back as redis.Nil.
	err = a.redis.SetArgs(ctx, posterUploadKeyPrefix+token, body, redis.SetArgs{
		Mode: "NX",
		TTL:  posterUploadTTL,
	}).Err()
	if errors.Is(err, redis.Nil) {
		return "", errors.New("poster upload token collision")
	}
	if err != nil {
		return "", fmt.Errorf("storing poster upload grant: %w", err)
	}
	return token, nil
}

// redeemPosterUpload consumes a token and returns its grant. GETDEL is atomic,
// so exactly one of two racing redemptions gets the grant and the other gets
// ErrNotFound -- which is what makes "single use" true rather than aspirational.
func (a *App) redeemPosterUpload(ctx context.Context, token string) (*posterGrant, error) {
	if a.redis == nil {
		return nil, errUploadsUnconfigured
	}
	if token == "" {
		return nil, redis.Nil
	}
	body, err := a.redis.GetDel(ctx, posterUploadKeyPrefix+token).Bytes()
	if err != nil {
		return nil, err
	}
	var g posterGrant
	if err := json.Unmarshal(body, &g); err != nil {
		return nil, fmt.Errorf("decoding poster upload grant: %w", err)
	}
	return &g, nil
}

// PosterUploadURL is the address a minted token is redeemed at. The event id
// is in the path as well as in the grant so the URL says what it does and a
// mismatch fails loudly; the token remains the only thing that authorises it.
func PosterUploadURL(baseURL, tenantSlug string, eventID uuid.UUID, token string) string {
	return fmt.Sprintf("%s/%s/api/events/%s/poster/upload?t=%s",
		strings.TrimSuffix(baseURL, "/"), tenantSlug, eventID, url.QueryEscape(token))
}

// posterUploadOffer mints a link for one event and renders the two lines a
// tool caller needs: the URL and how to POST to it. Returns "" when links are
// unavailable, because a poster link is a convenience hung off create_event --
// failing to mint one must never fail the create itself.
func (a *App) posterUploadOffer(ctx context.Context, tenantID, userID, eventID uuid.UUID) string {
	url, err := a.PosterUploadLink(ctx, tenantID, userID, eventID)
	if err != nil {
		return ""
	}
	return "\nPoster: none yet. Upload one within 15 minutes, once, with\n" +
		"  curl -F poster=@poster.jpg '" + url + "'\n"
}

// PosterUploadLink mints a one-time upload URL for an event. Exported so the
// tool layer can offer one on demand as well as after a create.
func (a *App) PosterUploadLink(ctx context.Context, tenantID, userID, eventID uuid.UUID) (string, error) {
	// Both cheap preconditions first, so an unconfigured Kit fails without a
	// database round-trip -- and so this is safe to call on a bare App.
	if a.redis == nil {
		return "", errUploadsUnconfigured
	}
	if a.baseURL == "" {
		return "", errors.New("poster upload links need BASE_URL")
	}
	slug, err := a.tenantSlug(ctx, tenantID)
	if err != nil {
		return "", err
	}
	token, err := a.mintPosterUpload(ctx, posterGrant{TenantID: tenantID, UserID: userID, EventID: eventID})
	if err != nil {
		return "", err
	}
	return PosterUploadURL(a.baseURL, slug, eventID, token), nil
}

// registerPosterUploadRoutes mounts the redemption endpoint.
//
// Deliberately NOT behind console.JSON: the whole point is a caller with no
// session and no CSRF token. It carries {slug} so it still goes through tenant
// resolution and the app-enablement gate, and the token is checked against
// that resolved tenant -- a grant minted in one workspace cannot be redeemed
// against another's URL.
func registerPosterUploadRoutes(mux apps.Mux, a *App) {
	mux.Handle("POST /{slug}/api/events/{id}/poster/upload",
		auth.TenantFromPath(a.pool)(http.HandlerFunc(a.handleTokenUploadPoster)))
}

func (a *App) handleTokenUploadPoster(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenant := auth.TenantFromContext(ctx)
	if tenant == nil {
		http.NotFound(w, r)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		eventsErr(w, http.StatusBadRequest, "invalid event id")
		return
	}
	if a.redis == nil {
		eventsErr(w, http.StatusInternalServerError, errUploadsUnconfigured.Error())
		return
	}

	// The token is spent before anything else is checked. A caller who
	// presents a valid token and then trips a binding check has still burned
	// it: retrying a rejected upload with the same link must not be possible,
	// or "single use" turns into "single SUCCESS", which is a different and
	// much weaker promise.
	grant, err := a.redeemPosterUpload(ctx, r.URL.Query().Get("t"))
	if err != nil {
		if errors.Is(err, redis.Nil) {
			// One message for expired, already-used and never-existed alike:
			// distinguishing them tells a probing caller which tokens were
			// once real.
			eventsErr(w, http.StatusUnauthorized, "that upload link has expired or has already been used")
			return
		}
		a.serviceErr(w, fmt.Errorf("redeeming poster upload: %w", err))
		return
	}
	if grant.TenantID != tenant.ID || grant.EventID != id {
		eventsErr(w, http.StatusForbidden, "that upload link is for a different event")
		return
	}

	a.storePosterUpload(w, r, grant.TenantID, grant.UserID, grant.EventID)
}
