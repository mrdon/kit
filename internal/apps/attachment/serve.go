package attachment

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"

	store "github.com/mrdon/kit/internal/attachment"
)

// serveTTL is how long a minted thumbnail URL stays valid. The token is
// repeatable within this window (Verify does not consume it), so it works
// from an <img src> that re-fetches.
const serveTTL = 6 * time.Hour

// SignedURL returns a slug-relative URL that serves the attachment's bytes,
// authenticated by a signed token rather than a session/CSRF pair — so it
// works directly as an <img src>. Returns "" if the signer is unconfigured.
func (a *App) SignedURL(slug string, userID, tenantID, attachmentID uuid.UUID) string {
	if a.deepLinks == nil {
		return ""
	}
	tok, err := a.deepLinks.Sign(userID, tenantID, attachmentID, serveTTL)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("/%s/apps/attachment/%s?t=%s", slug, attachmentID, url.QueryEscape(tok))
}

// handleServe streams a decrypted attachment, authenticated solely by the
// signed token in ?t=. The token binds (user, tenant, attachment); we trust
// its tenant and require its bound entry to match the requested id.
func (a *App) handleServe(w http.ResponseWriter, r *http.Request) {
	if a.deepLinks == nil {
		http.Error(w, "attachments not configured", http.StatusInternalServerError)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	claims, err := a.deepLinks.Verify(r.URL.Query().Get("t"))
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if claims.EntryID != id {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	svc := a.service()
	if svc == nil {
		http.Error(w, "attachments not configured", http.StatusInternalServerError)
		return
	}
	meta, raw, err := svc.Load(r.Context(), claims.TenantID, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", meta.Mime)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(raw)
}
