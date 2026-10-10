package posters

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/console"
	"github.com/mrdon/kit/internal/auth"
)

// The console JSON API. Posters, templates and photos are a shared team
// surface (console.JSON: a caller, not an admin); settings are admin-only.
func registerConsoleRoutes(mux apps.Mux, a *App) {
	route := func(h http.HandlerFunc) http.Handler { return console.JSON(a.pool, a.signer, h) }
	admin := func(h http.HandlerFunc) http.Handler { return console.AdminJSON(a.pool, a.signer, h) }

	mux.Handle("GET /{slug}/api/posters", route(a.handleListPosters))
	mux.Handle("POST /{slug}/api/posters/generate", route(a.handleGenerate))
	mux.Handle("GET /{slug}/api/posters/for-event/{event}", route(a.handlePosterForEvent))
	mux.Handle("GET /{slug}/api/posters/{id}", route(a.handleGetPoster))
	mux.Handle("DELETE /{slug}/api/posters/{id}", route(a.handleDeletePoster))
	mux.Handle("POST /{slug}/api/posters/{id}/pick", route(a.handlePick))
	mux.Handle("POST /{slug}/api/posters/{id}/current", route(a.handleSetCurrent))
	mux.Handle("POST /{slug}/api/posters/{id}/update-facts", route(a.handleUpdateFacts))
	mux.Handle("POST /{slug}/api/posters/{id}/save-template", route(a.handleSaveTemplate))
	mux.Handle("GET /{slug}/api/posters/{id}/versions/{vid}/render", route(a.handleRenderVersion))

	mux.Handle("GET /{slug}/api/posters/templates", route(a.handleListTemplates))
	mux.Handle("POST /{slug}/api/posters/templates", route(a.handleCreateTemplate))
	mux.Handle("GET /{slug}/api/posters/templates/{id}", route(a.handleGetTemplate))
	mux.Handle("POST /{slug}/api/posters/templates/{id}/status", route(a.handleTemplateStatus))
	mux.Handle("POST /{slug}/api/posters/templates/{id}/rename", route(a.handleRenameTemplate))
	mux.Handle("POST /{slug}/api/posters/templates/{id}/rollback", route(a.handleRollbackTemplate))
	mux.Handle("GET /{slug}/api/posters/templates/{id}/render", route(a.handleRenderTemplate))
	mux.Handle("GET /{slug}/api/posters/templates/{id}/reference", route(a.handleTemplateReference))

	mux.Handle("GET /{slug}/api/posters/photos", route(a.handleListPhotos))
	mux.Handle("PATCH /{slug}/api/posters/photos/{id}", route(a.handleUpdatePhoto))
	mux.Handle("GET /{slug}/api/posters/photos/{id}/image", route(a.handlePhotoImage))

	mux.Handle("GET /{slug}/api/posters/settings", admin(a.handleGetSettings))
	mux.Handle("PUT /{slug}/api/posters/settings", admin(a.handleSaveSettings))
	mux.Handle("POST /{slug}/api/posters/settings/sync", admin(a.handleSyncNow))
	mux.Handle("POST /{slug}/api/posters/settings/rederive", admin(a.handleRederive))
}

// The response and request helpers are the console package's; these
// names keep the handlers short.
var (
	writeJSON = console.WriteJSON
	writeErr  = console.WriteErr
	pathID    = console.PathUUID
)

func readBody(w http.ResponseWriter, r *http.Request, v any) bool {
	return console.ReadJSON(w, r, v, 2<<20)
}

// posterView is a poster as the list and detail pages show it.
type posterView struct {
	Poster
	EventTitle string       `json:"event_title,omitempty"`
	EventSlug  string       `json:"event_slug,omitempty"`
	Status     PosterStatus `json:"status"`
	// Thumb is the current version's portrait render URL, for lists.
	Thumb string `json:"thumb,omitempty"`
}

func (a *App) posterView(r *http.Request, p *Poster) posterView {
	v := posterView{Poster: *p, Status: posterStatus(p)}
	if p.EventID != nil {
		if evs := a.eventsService(); evs != nil {
			if ev, err := evs.Get(r.Context(), p.TenantID, *p.EventID); err == nil {
				v.EventTitle, v.EventSlug = ev.Title, ev.Slug
			}
		}
	}
	if p.CurrentVersionID != nil {
		v.Thumb = a.renderPath(r, p.ID, *p.CurrentVersionID, "")
	}
	return v
}

func (a *App) renderPath(r *http.Request, posterID, versionID uuid.UUID, format string) string {
	path := fmt.Sprintf("/%s/api/posters/%s/versions/%s/render", r.PathValue("slug"), posterID, versionID)
	if format != "" {
		path += "?format=" + format
	}
	return path
}

func (a *App) handleListPosters(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	posters, err := listPosters(r.Context(), a.pool, caller.TenantID)
	if err != nil {
		a.httpErr(w, err)
		return
	}
	out := make([]posterView, 0, len(posters))
	for i := range posters {
		out = append(out, a.posterView(r, &posters[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"posters": out, "renderer_ready": a.RendererReady(r.Context())})
}

// handlePosterForEvent is the drawer's view: the event's poster, its latest
// option batch and whether it is stale.
func (a *App) handlePosterForEvent(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	eventID, ok := pathID(w, r, "event")
	if !ok {
		return
	}
	poster, err := posterForEvent(r.Context(), a.pool, caller.TenantID, eventID)
	if err != nil {
		a.httpErr(w, err)
		return
	}
	resp := map[string]any{"poster": nil, "options": []any{}, "renderer_ready": a.RendererReady(r.Context()), "renderer_configured": a.renderer != nil, "brand_ready": true}
	if _, err := a.brandFor(r.Context(), caller.TenantID); err != nil {
		resp["brand_ready"] = false
		resp["brand_problem"] = strings.TrimPrefix(err.Error(), "no brand: ")
	}
	if poster == nil {
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp["poster"] = a.posterView(r, poster)
	resp["options"] = a.latestOptions(r, poster)
	writeJSON(w, http.StatusOK, resp)
}

// optionView is one option in a batch with its render URL.
type optionView struct {
	Version
	Thumb string `json:"thumb"`
}

func (a *App) latestOptions(r *http.Request, poster *Poster) []optionView {
	versions, err := listVersions(r.Context(), a.pool, poster.TenantID, poster.ID, true)
	if err != nil {
		return nil
	}
	var batch *uuid.UUID
	for _, v := range versions {
		if v.BatchID != nil {
			batch = v.BatchID
			break
		}
	}
	if batch == nil {
		return nil
	}
	return a.optionViews(r, poster, *batch)
}

func (a *App) optionViews(r *http.Request, poster *Poster, batch uuid.UUID) []optionView {
	vs, err := listBatch(r.Context(), a.pool, poster.TenantID, batch)
	if err != nil {
		return nil
	}
	out := make([]optionView, 0, len(vs))
	for _, v := range vs {
		out = append(out, optionView{Version: v, Thumb: a.renderPath(r, poster.ID, v.ID, "")})
	}
	return out
}

func (a *App) handleGetPoster(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	poster, cur, err := a.loadPoster(r.Context(), caller.TenantID, id)
	if err != nil {
		a.httpErr(w, err)
		return
	}
	versions, err := listVersions(r.Context(), a.pool, caller.TenantID, id, false)
	if err != nil {
		a.httpErr(w, err)
		return
	}
	formats := []string{}
	portrait := ""
	if brand, err := a.brandFor(r.Context(), caller.TenantID); err == nil {
		formats, portrait = sortedKeys(brand.Formats), brand.PortraitFormat
	}
	vviews := make([]optionView, 0, len(versions))
	for _, v := range versions {
		vviews = append(vviews, optionView{Version: v, Thumb: a.renderPath(r, poster.ID, v.ID, "")})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"poster": a.posterView(r, poster), "current": cur, "versions": vviews,
		"options": a.latestOptions(r, poster), "formats": formats, "portrait_format": portrait,
		"renderer_ready": a.RendererReady(r.Context()),
	})
}

func (a *App) handleDeletePoster(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := deletePoster(r.Context(), a.pool, caller.TenantID, id); err != nil {
		a.httpErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type versionBody struct {
	VersionID string `json:"version_id"`
}

func (a *App) handlePick(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var body versionBody
	if !readBody(w, r, &body) {
		return
	}
	vid, err := uuid.Parse(body.VersionID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "version_id is required")
		return
	}
	poster, err := a.Pick(r.Context(), caller.TenantID, caller.UserID, id, vid)
	if err != nil {
		a.httpErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"poster": a.posterView(r, poster)})
}

// handleSetCurrent is undo/redo/branch: any version of the poster can
// become the current one; the next edit descends from it.
func (a *App) handleSetCurrent(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var body versionBody
	if !readBody(w, r, &body) {
		return
	}
	vid, err := uuid.Parse(body.VersionID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "version_id is required")
		return
	}
	v, err := getVersion(r.Context(), a.pool, caller.TenantID, vid)
	if err != nil || v.PosterID != id {
		writeErr(w, http.StatusNotFound, "version not found")
		return
	}
	if err := setCurrentVersion(r.Context(), a.pool, caller.TenantID, id, vid); err != nil {
		a.httpErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"current": v})
}

func (a *App) handleUpdateFacts(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	res, changes, err := a.UpdateFacts(r.Context(), caller.TenantID, id, AuthorUser)
	if err != nil {
		a.httpErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": res.Version, "problems": res.Problems, "changes": changes})
}

// handleRenderVersion serves a version's render at a format, from the
// cache or fresh. ?download=1 sets a filename.
func (a *App) handleRenderVersion(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	vid, ok := pathID(w, r, "vid")
	if !ok {
		return
	}
	v, err := getVersion(r.Context(), a.pool, caller.TenantID, vid)
	if err != nil || v.PosterID != id {
		http.NotFound(w, r)
		return
	}
	format := r.URL.Query().Get("format")
	png, problems, err := a.renderVersion(r.Context(), caller.TenantID, v, format)
	if err != nil {
		a.httpErr(w, err)
		return
	}
	if len(problems) > 0 {
		writeErr(w, http.StatusUnprocessableEntity, strings.Join(problems, "; "))
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	if r.URL.Query().Get("download") == "1" {
		name := fmt.Sprintf("poster-%s-%s.png", strOr(format, "portrait"), v.CreatedAt.Format("20060102-1504"))
		w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(name))
	}
	http.ServeContent(w, r, "poster.png", time.Time{}, strings.NewReader(string(png)))
}
