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

type photoBody struct {
	Description *string  `json:"description"`
	Tags        []string `json:"tags"`
	FocusX      *float64 `json:"focus_x"`
	FocusY      *float64 `json:"focus_y"`
	Notes       *string  `json:"notes"`
}

// handleUpdatePhoto edits the index by hand. A pending photo given a
// description becomes indexed, same as through the MCP tools.
func (a *App) handleUpdatePhoto(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var body photoBody
	if !readBody(w, r, &body) {
		return
	}
	cur, err := getPhoto(r.Context(), a.pool, caller.TenantID, id)
	if err != nil {
		a.httpErr(w, err)
		return
	}
	entry := IndexEntry{ID: id, Description: cur.Description, Tags: cur.Tags, FocusX: cur.FocusX, FocusY: cur.FocusY, Notes: cur.Notes}
	if body.Description != nil {
		entry.Description = *body.Description
	}
	if body.Tags != nil {
		entry.Tags = body.Tags
	}
	if body.FocusX != nil {
		entry.FocusX = *body.FocusX
	}
	if body.FocusY != nil {
		entry.FocusY = *body.FocusY
	}
	if body.Notes != nil {
		entry.Notes = *body.Notes
	}
	// Without a description the photo stays pending: focus, tags and notes
	// are hints, and only a description makes a photo searchable.
	var p *Photo
	if strings.TrimSpace(entry.Description) == "" {
		p, err = setPhotoHints(r.Context(), a.pool, caller.TenantID, entry)
	} else {
		p, err = indexPhoto(r.Context(), a.pool, caller.TenantID, entry, "console:"+caller.Identity)
	}
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
