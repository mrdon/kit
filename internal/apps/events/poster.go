package events

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/attachment"
	"github.com/mrdon/kit/internal/auth"
)

// Event posters.
//
// A poster is the graphic that already gets made for every event -- the thing
// that goes in the newsletter -- so the event form asks for it rather than the
// site inventing a stock photo.
//
// THE SERVE ROUTE IS FOR BUILDS, NOT BROWSERS. The website downloads the image
// once at build time and republishes it from its own domain, so the public
// site carries no link back to Kit: it keeps working when Kit is down, and the
// brewery's pages do not quietly depend on our uptime or leak our hostname.
//
// That makes the endpoint a build-time API, so it is authenticated exactly
// like the feed it accompanies -- same bearer token, same lifetime. It is NOT
// a public URL. Two gates apply, and both matter:
//
//   - the feed token, so the endpoint is not open to the world; and
//   - IsPubliclyVisible, so even a token holder cannot pull the poster for a
//     draft or a private booking.
//
// The second is the one that protects a customer, and it is the same single
// predicate the feed uses.

// posterMaxBytes caps an upload. Posters are print-ish JPEGs; anything larger
// is a mistake rather than a poster.
const posterMaxBytes = 8 << 20

// allowedPosterMIME is an allowlist, not a blocklist: this file is served back
// to browsers, so an SVG (which can carry script) or an HTML file mislabelled
// as an image is a stored-XSS vector.
var allowedPosterMIME = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
	"image/gif":  ".gif",
}

func (a *App) handleUploadPoster(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		eventsErr(w, http.StatusBadRequest, "invalid event id")
		return
	}
	store := a.attachments()
	if store == nil {
		eventsErr(w, http.StatusInternalServerError, "attachments are not configured")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, posterMaxBytes+(1<<20))
	if err := r.ParseMultipartForm(posterMaxBytes + (1 << 20)); err != nil {
		eventsErr(w, http.StatusBadRequest, "poster upload too large or malformed")
		return
	}
	file, header, err := r.FormFile("poster")
	if err != nil {
		eventsErr(w, http.StatusBadRequest, "a poster image is required")
		return
	}
	defer file.Close()

	raw := make([]byte, 0, header.Size)
	buf := make([]byte, 32*1024)
	for {
		n, rerr := file.Read(buf)
		raw = append(raw, buf[:n]...)
		if rerr != nil {
			break
		}
		if len(raw) > posterMaxBytes {
			eventsErr(w, http.StatusBadRequest, "poster is larger than 8MB")
			return
		}
	}

	// Sniff rather than trust the declared type: the filename and the
	// Content-Type header are both attacker-controlled.
	mime := http.DetectContentType(raw)
	if i := strings.IndexByte(mime, ';'); i >= 0 {
		mime = strings.TrimSpace(mime[:i])
	}
	if _, ok := allowedPosterMIME[mime]; !ok {
		eventsErr(w, http.StatusBadRequest, fmt.Sprintf(
			"that file is a %s. Posters must be a JPEG, PNG, WebP or GIF image.", mime))
		return
	}

	att, err := store.Store(r.Context(), caller.TenantID, caller.UserID, header.Filename, mime, raw)
	if err != nil {
		a.serviceErr(w, fmt.Errorf("storing poster: %w", err))
		return
	}
	ev, err := a.svc.Update(r.Context(), caller.TenantID, id, UpdateParams{HeroAttachmentID: &att.ID})
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	eventsJSON(w, http.StatusOK, map[string]any{"event": ev})
}

func (a *App) handleDeletePoster(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		eventsErr(w, http.StatusBadRequest, "invalid event id")
		return
	}
	ev, err := a.svc.Update(r.Context(), caller.TenantID, id, UpdateParams{ClearHero: true})
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	eventsJSON(w, http.StatusOK, map[string]any{"event": ev})
}

// handleServePoster streams a poster to a site build. See the note at the top
// of this file: bearer token first, then IsPubliclyVisible.
func (a *App) handleServePoster(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenant := auth.TenantFromContext(ctx)
	if tenant == nil {
		http.NotFound(w, r)
		return
	}
	settings, err := getSettings(ctx, a.pool, tenant.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if settings.FeedToken == "" || !validFeedToken(r, settings.FeedToken) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="events feed"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ev, err := getEventBySlug(ctx, a.pool, tenant.ID, r.PathValue("event"))
	if err != nil || ev == nil {
		http.NotFound(w, r)
		return
	}
	// 404 rather than 403 for a non-public event: a 403 would confirm that an
	// event with that slug exists, which is itself a leak for a private booking
	// named after a customer.
	if !ev.IsPubliclyVisible() || ev.HeroAttachmentID == nil {
		http.NotFound(w, r)
		return
	}
	store := a.attachments()
	if store == nil {
		http.NotFound(w, r)
		return
	}
	meta, raw, err := store.Load(ctx, tenant.ID, *ev.HeroAttachmentID)
	if err != nil {
		if errors.Is(err, attachment.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	ct := meta.Mime
	if _, ok := allowedPosterMIME[ct]; !ok {
		// Stored before the allowlist existed, or stored by another path.
		// Refuse to hand a browser something we would not accept today.
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if _, err := w.Write(raw); err != nil {
		return
	}
}

// handleConsolePoster streams a poster to an authenticated console user for
// ANY status. The public route deliberately 404s drafts, but a poster is
// normally attached while the event is still a draft -- so without this the
// form could never show you what you just uploaded.
func (a *App) handleConsolePoster(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ev, err := getEvent(r.Context(), a.pool, caller.TenantID, id)
	if err != nil || ev == nil || ev.HeroAttachmentID == nil {
		http.NotFound(w, r)
		return
	}
	store := a.attachments()
	if store == nil {
		http.NotFound(w, r)
		return
	}
	meta, raw, err := store.Load(r.Context(), caller.TenantID, *ev.HeroAttachmentID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, ok := allowedPosterMIME[meta.Mime]; !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", meta.Mime)
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(raw)
}

// PosterURL is the build-time URL for an event's poster. Fetching it needs the
// feed bearer token; it is not a public link.
func PosterURL(baseURL, tenantSlug, eventSlug string) string {
	return fmt.Sprintf("%s/%s/events/%s/poster", strings.TrimSuffix(baseURL, "/"), tenantSlug, eventSlug)
}
