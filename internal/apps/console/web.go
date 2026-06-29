package console

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/admin"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/models"
	consoleweb "github.com/mrdon/kit/web/console"
)

// handleShell serves the console SPA HTML with per-workspace title and
// favicon substituted in. Every client-side route resolves to these same
// bytes; the React router takes over once loaded.
func (a *App) handleShell(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.Error(w, "tenant not resolved", http.StatusInternalServerError)
		return
	}
	title := tenant.Name
	if title == "" {
		title = tenant.Slug
	}
	body, err := consoleweb.IndexHTML(tenant.Slug, title)
	if err != nil {
		http.Error(w, "console not built", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(body)
}

// meResponse drives the console top bar and launcher gating. Admin
// gating is cosmetic — admin APIs 403 regardless. Workspace name/icon and
// the logout URL feed the top bar (mirrors the chrome.Header the vanilla
// pages render).
type meResponse struct {
	UserID        string   `json:"user_id"`
	DisplayName   string   `json:"display_name"`
	IsAdmin       bool     `json:"is_admin"`
	WorkspaceName string   `json:"workspace_name"`
	WorkspaceIcon string   `json:"workspace_icon_url"`
	LogoutURL     string   `json:"logout_url"`
	DisabledApps  []string `json:"disabled_apps"`
}

func (a *App) handleMe(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	tenant := auth.TenantFromContext(r.Context())
	if caller == nil || tenant == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	name := caller.Identity
	if u, err := models.GetUserByID(r.Context(), a.pool, caller.TenantID, caller.UserID); err == nil && u != nil {
		if u.DisplayName != nil && *u.DisplayName != "" {
			name = *u.DisplayName
		}
	}
	wsName := tenant.Name
	if wsName == "" {
		wsName = tenant.Slug
	}
	// Disabled apps drive client-side nav/launcher filtering so a turned-off
	// app's tiles and links disappear for everyone (server still 404s its
	// routes regardless). Best-effort: on error we just show everything.
	disabled := make([]string, 0)
	if set, err := apps.DisabledApps(r.Context(), caller.TenantID); err == nil {
		for name := range set {
			disabled = append(disabled, name)
		}
	}
	writeJSON(w, http.StatusOK, meResponse{
		UserID:        caller.UserID.String(),
		DisplayName:   name,
		IsAdmin:       caller.IsAdmin,
		WorkspaceName: wsName,
		WorkspaceIcon: "/" + tenant.Slug + "/icon-192.png",
		LogoutURL:     "/" + tenant.Slug + "/logout",
		DisabledApps:  disabled,
	})
}

// integrationRow is the JSON shape the console integrations page renders.
// Mirrors admin's server-rendered view-model (internal/apps/admin/web.go).
type integrationRow struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Slug        string `json:"slug"`
	Connected   bool   `json:"connected"`
	Detail      string `json:"detail"`
	StatusError string `json:"status_error"`
	ManageURL   string `json:"manage_url"`
}

func (a *App) handleIntegrations(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	caller := auth.CallerFromContext(r.Context())
	if tenant == nil || caller == nil {
		http.Error(w, "tenant or caller not resolved", http.StatusInternalServerError)
		return
	}

	registered := admin.Integrations()
	rows := make([]integrationRow, 0, len(registered))
	for _, integ := range registered {
		row := integrationRow{
			Name:        integ.Name(),
			Description: integ.Description(),
			Slug:        integ.Slug(),
			ManageURL:   integ.ManageURL(tenant.Slug),
		}
		status, err := integ.Status(r.Context(), caller.TenantID)
		if err != nil {
			slog.Warn("console: integration status",
				"slug", integ.Slug(), "tenant_id", caller.TenantID, "error", err)
			row.StatusError = "Could not load status."
		} else {
			row.Connected = status.Connected
			row.Detail = status.Detail
		}
		rows = append(rows, row)
	}
	writeJSON(w, http.StatusOK, rows)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
