// Package app wraps the PWA's built assets so the Go binary can serve
// them via //go:embed. The dist/ directory is produced by `make app-build`.
package app

import (
	"embed"
	"io"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// Handler serves the embedded PWA with SPA fallback — paths without a file
// extension that don't exist on disk get index.html so client-side routing
// works (react-router is configured with basename=/app).
//
// If the frontend hasn't been built yet, returns a 503 handler with a hint
// to run `make app-build`. That keeps `go build` working in fresh clones
// without requiring node.
//
// We don't use http.FileServer because it auto-redirects "*/index.html"
// to "*/" which breaks client-side route fallback. Directly serving bytes
// is simpler here.
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return placeholder()
	}
	indexBytes, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		return placeholder()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Strip "/app" prefix to get the path inside dist/.
		clean := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/app"), "/")
		if clean == "" {
			writeIndex(w, indexBytes)
			return
		}
		// Try to open the requested file. If it doesn't exist and the path
		// doesn't look like an asset (no extension), fall back to index.
		f, err := sub.Open(clean)
		if err != nil {
			if !looksLikeAsset(clean) {
				writeIndex(w, indexBytes)
				return
			}
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		stat, err := f.Stat()
		if err != nil || stat.IsDir() {
			writeIndex(w, indexBytes)
			return
		}
		w.Header().Set("Content-Type", contentType(clean))
		_, _ = io.Copy(w, f)
	})
}

func writeIndex(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(body)
}

// contentType picks a Content-Type by file extension. We keep this tiny
// instead of importing mime because the PWA only produces a handful of
// asset types.
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

// looksLikeAsset returns true if the path looks like a file (has an
// extension in the final segment). Used to avoid sending index.html for
// requests that are clearly for static assets.
func looksLikeAsset(p string) bool {
	slash := strings.LastIndex(p, "/")
	dot := strings.LastIndex(p, ".")
	return dot > slash
}

func placeholder() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Kit PWA not built. Run `make app-build`.", http.StatusServiceUnavailable)
	})
}
