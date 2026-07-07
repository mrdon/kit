package web

import (
	"embed"
	"html/template"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
)

//go:embed templates/landing.html
var landingFS embed.FS

var landingTmpl = template.Must(template.ParseFS(landingFS, "templates/landing.html"))

// workspaceLink is one tenant rendered in the landing page's workspace list.
type workspaceLink struct {
	Name    string
	Slug    string
	Initial string // first letter, for the icon fallback
	HasIcon bool
}

type landingData struct {
	BaseURL    string
	Workspaces []workspaceLink
}

// NewLandingHandler creates a handler that serves the landing page. It lists
// every tenant as a directory of workspaces — each links to the web console,
// with a secondary link to the card stack. No login detection: clicking a
// workspace hits its per-slug session, which redirects to that workspace's
// login if the browser isn't already authenticated there.
func NewLandingHandler(baseURL string, pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		data := landingData{BaseURL: baseURL}
		tenants, err := models.ListAllTenants(r.Context(), pool)
		if err != nil {
			// Degrade to the plain marketing page rather than 500 — the
			// workspace list is a convenience, not the point of the page.
			slog.Error("landing: listing tenants", "error", err)
		} else {
			for _, t := range tenants {
				if !models.IsValidSlug(t.Slug) {
					continue
				}
				data.Workspaces = append(data.Workspaces, workspaceLink{
					Name:    t.Name,
					Slug:    t.Slug,
					Initial: initial(t.Name),
					HasIcon: len(t.Icon192) > 0,
				})
			}
			sort.Slice(data.Workspaces, func(i, j int) bool {
				return strings.ToLower(data.Workspaces[i].Name) < strings.ToLower(data.Workspaces[j].Name)
			})
		}
		if err := landingTmpl.Execute(w, data); err != nil {
			slog.Error("landing: rendering template", "error", err)
		}
	}
}

// initial returns the uppercased first letter of a name for the icon
// fallback, or "?" if the name is empty.
func initial(name string) string {
	for _, r := range strings.TrimSpace(name) {
		return strings.ToUpper(string(r))
	}
	return "?"
}
