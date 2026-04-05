package web

import (
	"embed"
	"html/template"
	"net/http"
)

//go:embed templates/landing.html
var landingFS embed.FS

var landingTmpl = template.Must(template.ParseFS(landingFS, "templates/landing.html"))

// NewLandingHandler creates a handler that serves the landing page with the given base URL.
func NewLandingHandler(baseURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		landingTmpl.Execute(w, map[string]string{"BaseURL": baseURL})
	}
}
