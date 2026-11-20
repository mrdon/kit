package trivia

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mrdon/kit/internal/auth"
)

// The lobby video: the Admin setting that names it, and the route the TV
// plays it from. See explainer.go for why Kit serves its own copy.

// explainerPath is where the TV loads the video. One per workspace, so the
// rendered page only has to know whether there is one.
func explainerPath(slug string) string { return "/" + slug + "/trivia/explainer.mp4" }

// ConfigureExplainer hands the app the Drive API key. Without one the video
// still works for a publicly shared file; Kit just cannot see when it
// changed, so it re-downloads hourly instead of checking every few minutes.
func ConfigureExplainer(driveKey string) {
	if instance == nil {
		return
	}
	instance.explainer = newExplainerCache(explainerCacheDir(), driveKey)
}

func explainerCacheDir() string { return filepath.Join(os.TempDir(), "kit-trivia-explainer") }

// explainerCache is the configured cache, or a keyless one for an app that
// was never configured (tests, a dev box without the key).
func (a *App) explainerCache() *explainerCache {
	a.explainerOnce.Do(func() {
		if a.explainer == nil {
			a.explainer = newExplainerCache(explainerCacheDir(), "")
		}
	})
	return a.explainer
}

type explainerPayload struct {
	// Link is what the admin pasted, "" when there is no video.
	Link string `json:"link"`
	// SizeMB is the size of the copy Kit holds, so the page can confirm it
	// really fetched something. Zero when there is no link.
	SizeMB float64 `json:"size_mb,omitempty"`
}

func (a *App) handleGetExplainer(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	link, err := GetExplainerLink(r.Context(), a.pool, caller.TenantID)
	if err != nil {
		serverError(w, "loading trivia explainer link", err)
		return
	}
	writeJSON(w, explainerPayload{Link: link})
}

// handleSaveExplainer stores a link, or clears it with "". A link is fetched
// before it is saved, so a file that is not shared publicly is refused now,
// while an admin is looking, rather than leaving a blank box on the TV on
// quiz night. The fetch also warms the cache for the first screen.
func (a *App) handleSaveExplainer(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	var body struct {
		Link string `json:"link"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		clientError(w, r, http.StatusBadRequest, "invalid JSON")
		return
	}
	link := strings.TrimSpace(body.Link)
	out := explainerPayload{Link: link}
	if link != "" {
		id, err := driveFileID(link)
		if err != nil {
			clientError(w, r, http.StatusBadRequest, explainerMessage(err))
			return
		}
		got, err := a.explainerCache().Get(r.Context(), id)
		if err != nil {
			clientError(w, r, http.StatusBadRequest, explainerMessage(err))
			return
		}
		out.SizeMB = float64(got.size) / (1 << 20)
	}
	if err := SaveExplainerLink(r.Context(), a.pool, caller.TenantID, link); err != nil {
		serverError(w, "saving trivia explainer link", err)
		return
	}
	writeJSON(w, out)
}

// handleExplainerVideo serves the workspace's video to the TV, with range
// requests and Last-Modified handled by ServeContent. no-cache rather than a
// max-age, so a screen revalidates on each load and picks up a new version
// without anyone clearing its browser.
func (a *App) handleExplainerVideo(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.NotFound(w, r)
		return
	}
	link, err := GetExplainerLink(r.Context(), a.pool, tenant.ID)
	if err != nil {
		serverError(w, "loading trivia explainer link", err)
		return
	}
	id, err := driveFileID(link)
	if link == "" || err != nil {
		http.NotFound(w, r)
		return
	}
	got, err := a.explainerCache().Get(r.Context(), id)
	if err != nil {
		slog.Warn("fetching trivia explainer video", "tenant_id", tenant.ID, "error", err)
		http.Error(w, "video unavailable", http.StatusBadGateway)
		return
	}
	f, err := os.Open(got.path)
	if err != nil {
		serverError(w, "opening trivia explainer video", err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "explainer.mp4", got.modified, f)
}

// explainerMessage turns an error into a sentence for the settings page.
func explainerMessage(err error) string {
	msg := err.Error()
	r, n := utf8.DecodeRuneInString(msg)
	if n == 0 {
		return msg
	}
	msg = string(unicode.ToUpper(r)) + msg[n:]
	if !strings.HasSuffix(msg, ".") {
		msg += "."
	}
	return msg
}
