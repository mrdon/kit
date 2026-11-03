package menu

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/console"
	"github.com/mrdon/kit/internal/auth"
)

// The featured beer's photo picker on the printed menu settings page: the
// photos Kit can find for a beer, and a thumbnail of each to choose from.

func registerFeaturedRoutes(mux apps.Mux, a *App) {
	adminRoute := func(h http.HandlerFunc) http.Handler {
		return console.AdminJSON(a.pool, a.signer, h)
	}
	mux.Handle("GET /{slug}/api/menu/print/featured/photos", adminRoute(a.handleFeaturedPhotos))
	mux.Handle("GET /{slug}/api/menu/print/featured/photo", adminRoute(a.handleFeaturedPhoto))
}

// handleFeaturedPhotos lists the candidates for ?beer=, best first: the order
// "auto" would pick from.
func (a *App) handleFeaturedPhotos(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenant := auth.TenantFromContext(ctx)
	beer := strings.TrimSpace(r.URL.Query().Get("beer"))
	cands, err := featuredCandidates(ctx, a.pool, tenant.ID, beer)
	if err != nil {
		// The library half failed; what was found before it is still useful.
		slog.Warn("listing featured photos", "tenant_id", tenant.ID, "error", err)
	}
	if cands == nil {
		cands = []photoCandidate{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"candidates": cands})
}

// featuredThumbSize is the long edge of a picker thumbnail.
const featuredThumbSize = 300

// handleFeaturedPhoto serves the image behind ?ref=, an asset:<key> or a
// photo:<id>, for the picker. Library photos come from Drive through the
// poster renderer, at thumbnail size.
func (a *App) handleFeaturedPhoto(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenant := auth.TenantFromContext(ctx)
	ref := strings.TrimSpace(r.URL.Query().Get("ref"))

	var (
		raw  []byte
		mime = "image/jpeg"
		err  error
	)
	switch {
	case strings.HasPrefix(ref, AssetRef):
		var asset *Asset
		asset, err = loadPrintAsset(ctx, a.pool, tenant.ID, strings.TrimPrefix(ref, AssetRef))
		if err == nil {
			raw, mime = asset.Bytes, asset.Mime
		}
	case strings.HasPrefix(ref, photoRef):
		raw, err = libraryPhotoAt(ctx, tenant.ID, ref, featuredThumbSize)
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		slog.Warn("serving featured photo", "tenant_id", tenant.ID, "ref", ref, "error", err)
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = w.Write(raw)
}
