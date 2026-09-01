// Package console wraps the built console SPA assets so the Go binary can
// serve them via //go:embed. The dist/ directory is produced by
// `make console-build`. The console is the desktop, direct-manipulation
// web UI served at /{slug}/web/*; its shared bundle is served from
// /console/assets/* (distinct from the cards PWA's /app/assets/*).
package console

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// basePlaceholder is a literal in web/console/index.html the server
// rewrites to the per-workspace prefix at serve time. Vite-emitted tags
// keep their absolute /console/assets/... paths (shared bundle); the
// favicon link uses this token so it resolves to the workspace icon.
const basePlaceholder = "__KIT_BASE__"

// titlePlaceholder is rewritten to the workspace display name for <title>.
const titlePlaceholder = "__KIT_TITLE__"

// AssetHandler serves the shared console bundle under /console/assets/*.
// It does NOT cover the HTML entry point — that is routed per-workspace
// via /{slug}/web in internal/apps/console.
func AssetHandler() http.Handler {
	sub, err := distSubFS()
	if err != nil {
		return placeholder()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := strings.TrimPrefix(r.URL.Path, "/console/")
		if clean == "" || strings.HasSuffix(clean, "/") {
			http.NotFound(w, r)
			return
		}
		f, err := sub.Open(clean)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		stat, err := f.Stat()
		if err != nil || stat.IsDir() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType(clean))
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		body, err := fs.ReadFile(sub, clean)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	})
}

// IndexHTML returns the console entry-point HTML with the per-workspace
// slug and title substituted in. Same bytes for every request under
// /{slug}/web except for those tokens; the shared JS/CSS bundle lives at
// /console/assets/ and is referenced absolutely by the Vite build.
func IndexHTML(slug, title string) ([]byte, error) {
	return entryHTML("index.html", slug, title)
}

// PlayHTML returns the trivia player entry point with the per-workspace slug
// and title substituted in, exactly as IndexHTML does for the console.
//
// It is a SECOND Vite entry rather than a route inside the console SPA
// because the two have nothing in common: the console is authenticated,
// desktop and dense; this is a phone in a dark bar, held by somebody with no
// Kit account, and it must not carry the console's auth code, Shell or
// launcher. Two entries share one build and one hashed asset prefix.
func PlayHTML(slug, title string) ([]byte, error) {
	return entryHTML("play.html", slug, title)
}

// entryHTML is the shared body of IndexHTML and PlayHTML.
func entryHTML(name, slug, title string) ([]byte, error) {
	sub, err := distSubFS()
	if err != nil {
		return nil, err
	}
	raw, err := fs.ReadFile(sub, name)
	if err != nil {
		return nil, err
	}
	out := bytes.ReplaceAll(raw, []byte(basePlaceholder), []byte("/"+slug))
	out = bytes.ReplaceAll(out, []byte(titlePlaceholder), []byte(htmlEscape(title)))
	return out, nil
}

// htmlEscape escapes the minimal set needed for a plain <title> body.
func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// StaticFileHandler serves a single named file from the dist root (e.g.
// the vault SharedWorker, which Vite emits unhashed from public/). It is
// separate from AssetHandler, which only serves the hashed bundle under
// /console/assets/. Workers must not be cached aggressively — browsers
// re-fetch to detect updates — so this sets no-cache.
func StaticFileHandler(name, contentType string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sub, err := distSubFS()
		if err != nil {
			http.Error(w, "console not built", http.StatusServiceUnavailable)
			return
		}
		body, err := fs.ReadFile(sub, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(body)
	})
}

func distSubFS() (fs.FS, error) {
	return fs.Sub(distFS, "dist")
}

// contentType picks a Content-Type by file extension.
func contentType(name string) string {
	switch {
	case strings.HasSuffix(name, ".html"):
		return "text/html; charset=utf-8"
	case strings.HasSuffix(name, ".js"):
		return "application/javascript; charset=utf-8"
	case strings.HasSuffix(name, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(name, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(name, ".json"):
		return "application/json"
	case strings.HasSuffix(name, ".png"):
		return "image/png"
	default:
		return "application/octet-stream"
	}
}

func placeholder() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Kit console not built. Run `make console-build`.", http.StatusServiceUnavailable)
	})
}
