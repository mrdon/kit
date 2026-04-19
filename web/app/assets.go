// Package app wraps the PWA's built assets so the Go binary can serve
// them via //go:embed. The dist/ directory is produced by `make app-build`.
package app

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// basePlaceholder is a literal in web/app/index.html that the server
// rewrites to the per-workspace prefix at serve time. Vite-emitted tags
// keep their absolute /app/assets/... paths (shared bundle); the places
// that need to be workspace-scoped (manifest + icon links) use this token.
const basePlaceholder = "__KIT_BASE__"

// titlePlaceholder is rewritten to the workspace display name so the
// <title> matches the home-screen label Firefox Android generates when
// it falls back to a letter icon.
const titlePlaceholder = "__KIT_TITLE__"

// AssetHandler serves the shared PWA bundle under /app/assets/*. This
// does NOT cover the HTML entry point or any per-workspace artifacts —
// those are routed via /{slug}/... in internal/apps/cards/web.go.
func AssetHandler() http.Handler {
	sub, err := distSubFS()
	if err != nil {
		return placeholder()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := strings.TrimPrefix(r.URL.Path, "/app/")
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

// IndexHTML returns the PWA entry-point HTML with the per-workspace slug
// and title substituted in. Same bytes for every request under /{slug}/
// except for those tokens; the shared JS/CSS bundle lives at
// /app/assets/ and is referenced absolutely by the Vite build.
func IndexHTML(slug, title string) ([]byte, error) {
	sub, err := distSubFS()
	if err != nil {
		return nil, err
	}
	raw, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		return nil, err
	}
	out := bytes.ReplaceAll(raw, []byte(basePlaceholder), []byte("/"+slug))
	out = bytes.ReplaceAll(out, []byte(titlePlaceholder), []byte(htmlEscape(title)))
	return out, nil
}

// htmlEscape escapes the minimal set needed for a plain <title> body.
// Slack workspace names can contain "&" or "<" in theory; we don't want
// those to break parsing or enable injection.
func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// StaticFile returns a raw file from the PWA dist directory. Used for
// the service worker, the default Kit icon, and any other static asset
// that needs to be served from a workspace-scoped URL.
func StaticFile(name string) ([]byte, error) {
	sub, err := distSubFS()
	if err != nil {
		return nil, err
	}
	return fs.ReadFile(sub, name)
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
	case strings.HasSuffix(name, ".webmanifest"), strings.HasSuffix(name, ".json"):
		return "application/manifest+json"
	case strings.HasSuffix(name, ".png"):
		return "image/png"
	default:
		return "application/octet-stream"
	}
}

// ContentTypeFor is the exported form of contentType — used by handlers
// outside this package that need to echo the right media type.
func ContentTypeFor(name string) string { return contentType(name) }

func placeholder() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Kit PWA not built. Run `make app-build`.", http.StatusServiceUnavailable)
	})
}
