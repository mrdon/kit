package events

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/console"
	"github.com/mrdon/kit/internal/attachment"
	"github.com/mrdon/kit/internal/auth"
)

// The topper library's routes: a list and a PDF for the bar iPad, behind the
// topper capability like the week's card, and a one-time upload link for the
// agent or a harness to put a PDF in. The upload works like the poster link
// (see poster_upload.go): the URL is the credential, single use, 15 minutes.

// topperMaxBytes caps an upload at what the attachment store will hold. A 4x6
// card with a photo is a few MB at most.
const topperMaxBytes = attachment.MaxBytes

const topperUploadKeyPrefix = "kit:events:topper-upload:"

// topperGrant is what a topper upload token stands for. The title is bound at
// mint time, so the upload files under the name the tool was asked for.
type topperGrant struct {
	TenantID uuid.UUID `json:"tenant_id"`
	UserID   uuid.UUID `json:"user_id"`
	Title    string    `json:"title"`
}

func registerTopperLibraryRoutes(mux apps.Mux, a *App) {
	mux.Handle("GET /{slug}/api/events/topper-library",
		console.RequireCap(a.pool, a.signer, auth.CapEventsTopper, a.handleTopperLibrary))
	mux.Handle("GET /{slug}/events/topper-library/{id}/pdf",
		console.RequireCapPage(a.pool, a.signer, auth.CapEventsTopper, a.handleTopperLibraryPDF))
	mux.Handle("POST /{slug}/api/events/topper-library/upload",
		auth.TenantFromPath(a.pool)(http.HandlerFunc(a.handleTopperUpload)))
}

// TopperUploadLink mints a one-time link that files a PDF under title.
func (a *App) TopperUploadLink(ctx context.Context, tenantID, userID uuid.UUID, title string) (string, error) {
	if a.redis == nil {
		return "", errUploadsUnconfigured
	}
	if a.baseURL == "" {
		return "", errors.New("topper upload links need BASE_URL")
	}
	slug, err := a.tenantSlug(ctx, tenantID)
	if err != nil {
		return "", err
	}
	token, err := a.mintUploadGrant(ctx, topperUploadKeyPrefix,
		topperGrant{TenantID: tenantID, UserID: userID, Title: title})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/%s/api/events/topper-library/upload?t=%s",
		strings.TrimSuffix(a.baseURL, "/"), slug, url.QueryEscape(token)), nil
}

func (a *App) handleTopperLibrary(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.NotFound(w, r)
		return
	}
	toppers, err := listLibraryToppers(r.Context(), a.pool, tenant.ID)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	if toppers == nil {
		toppers = []LibraryTopper{}
	}
	eventsJSON(w, http.StatusOK, map[string]any{"toppers": toppers})
}

func (a *App) handleTopperLibraryPDF(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenant := auth.TenantFromContext(ctx)
	store := a.attachments()
	id, err := uuid.Parse(r.PathValue("id"))
	if tenant == nil || store == nil || err != nil {
		http.NotFound(w, r)
		return
	}
	t, err := getLibraryTopper(ctx, a.pool, tenant.ID, id)
	if errors.Is(err, errTopperNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		slog.Error("topper library: loading", "tenant_id", tenant.ID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	_, raw, err := store.Load(ctx, tenant.ID, t.AttachmentID)
	if err != nil {
		slog.Error("topper library: loading file", "tenant_id", tenant.ID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `inline; filename="`+topperFilename(t.Title)+`.pdf"`)
	// A replaced topper keeps its URL, so a cached copy would print the old one.
	w.Header().Set("Cache-Control", "no-store, must-revalidate")
	if _, err := w.Write(raw); err != nil {
		slog.Warn("topper library: writing response", "tenant_id", tenant.ID, "error", err)
	}
}

var nonFilename = regexp.MustCompile(`[^a-z0-9]+`)

func topperFilename(title string) string {
	s := strings.Trim(nonFilename.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if s == "" {
		return "topper"
	}
	return s
}

func (a *App) handleTopperUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenant := auth.TenantFromContext(ctx)
	store := a.attachments()
	if tenant == nil {
		http.NotFound(w, r)
		return
	}
	if a.redis == nil || store == nil {
		eventsErr(w, http.StatusInternalServerError, "topper uploads are not configured")
		return
	}
	// Spent before anything else is checked, as the poster link is: a rejected
	// upload must not be retryable on the same link.
	var g topperGrant
	if err := a.redeemUploadGrant(ctx, topperUploadKeyPrefix, r.URL.Query().Get("t"), &g); err != nil {
		if errors.Is(err, redis.Nil) {
			eventsErr(w, http.StatusUnauthorized, "that upload link has expired or has already been used")
			return
		}
		a.serviceErr(w, fmt.Errorf("redeeming topper upload: %w", err))
		return
	}
	if g.TenantID != tenant.ID {
		eventsErr(w, http.StatusForbidden, "that upload link is for a different workspace")
		return
	}

	raw, filename, msg := readTopperUpload(w, r)
	if msg != "" {
		eventsErr(w, http.StatusBadRequest, msg)
		return
	}
	att, err := store.Store(ctx, g.TenantID, g.UserID, filename, "application/pdf", raw)
	if err != nil {
		a.serviceErr(w, fmt.Errorf("storing topper: %w", err))
		return
	}
	t, replaced, err := putLibraryTopper(ctx, a.pool, g.TenantID, g.UserID, att.ID, g.Title)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	eventsJSON(w, http.StatusOK, map[string]any{"topper": t, "replaced": replaced})
}

// readTopperUpload reads the "pdf" form field and checks it is one. Returns a
// user-facing message instead of the bytes when it is not.
func readTopperUpload(w http.ResponseWriter, r *http.Request) ([]byte, string, string) {
	r.Body = http.MaxBytesReader(w, r.Body, topperMaxBytes+(1<<20))
	if err := r.ParseMultipartForm(topperMaxBytes + (1 << 20)); err != nil {
		return nil, "", "upload too large or malformed"
	}
	file, header, err := r.FormFile("pdf")
	if err != nil {
		return nil, "", "a PDF is required, as the form field \"pdf\""
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, topperMaxBytes+1))
	if err != nil {
		return nil, "", "could not read the upload"
	}
	if len(raw) > topperMaxBytes {
		return nil, "", "that PDF is larger than 10MB"
	}
	// Sniff rather than trust the declared type, which the caller controls.
	if mime := http.DetectContentType(raw); !strings.HasPrefix(mime, "application/pdf") {
		return nil, "", "that file is not a PDF. Toppers are stored as the PDF that gets printed."
	}
	return raw, header.Filename, ""
}
