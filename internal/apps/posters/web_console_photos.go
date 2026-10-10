package posters

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mrdon/kit/internal/auth"
)

// photoView adds the Drive thumbnail the console shows.
type photoView struct {
	Photo
	Thumb string `json:"thumb"`
}

func (a *App) handleListPhotos(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	q := r.URL.Query()
	photos, err := listPhotos(r.Context(), a.pool, caller.TenantID, PhotoFilter{Status: PhotoStatus(q.Get("status")), Folder: q.Get("folder")})
	if err != nil {
		a.httpErr(w, err)
		return
	}
	counts, err := photoCounts(r.Context(), a.pool, caller.TenantID)
	if err != nil {
		a.httpErr(w, err)
		return
	}
	all, err := listPhotos(r.Context(), a.pool, caller.TenantID, PhotoFilter{})
	if err != nil {
		a.httpErr(w, err)
		return
	}
	folderSet := map[string]bool{}
	for _, p := range all {
		folderSet[p.Folder] = true
	}
	folders := make([]string, 0, len(folderSet))
	for f := range folderSet {
		folders = append(folders, f)
	}
	sort.Strings(folders)
	out := make([]photoView, 0, len(photos))
	for _, p := range photos {
		out = append(out, photoView{Photo: p, Thumb: driveThumbnailURL(p.DriveFileID, 400)})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"photos":  out,
		"folders": folders,
		"counts":  map[string]int{"pending": counts[PhotoPending], "indexed": counts[PhotoIndexed], "removed": counts[PhotoRemoved]},
	})
}

// handleUpdatePhoto edits the index by hand, through the same path the
// update_photo_index tool uses, so the console and MCP agree on what a
// description-less edit means (hints saved, photo stays pending).
func (a *App) handleUpdatePhoto(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var body indexEntryArg
	if !readBody(w, r, &body) {
		return
	}
	body.ID = id.String()
	p, err := a.applyIndexEntry(r.Context(), caller.TenantID, body, "console:"+caller.Identity, false)
	if err != nil {
		a.httpErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"photo": photoView{Photo: *p, Thumb: driveThumbnailURL(p.DriveFileID, 400)}})
}

// handlePhotoImage proxies one photo through the renderer's cache, for the
// focus-point editor (Drive thumbnails are small and sometimes refused).
func (a *App) handlePhotoImage(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	p, err := getPhoto(r.Context(), a.pool, caller.TenantID, id)
	if err != nil {
		a.httpErr(w, err)
		return
	}
	if a.renderer == nil {
		writeErr(w, http.StatusServiceUnavailable, "the renderer is not configured")
		return
	}
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	if size <= 0 {
		size = 1024
	}
	jpeg, err := a.renderer.Photo(r.Context(), p.Ref(), size)
	if err != nil {
		a.httpErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeContent(w, r, "photo.jpg", time.Time{}, strings.NewReader(string(jpeg)))
}
