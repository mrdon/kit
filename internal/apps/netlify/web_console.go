package netlify

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/console"
	"github.com/mrdon/kit/internal/auth"
)

// registerConsoleRoutes wires the React console's JSON endpoints for the
// Website settings page (/{slug}/web/admin/netlify). The OAuth connect/callback
// dance stays as full-page routes in urls.go; only status + the two
// state-changing actions are JSON here. All admin-only.
func registerConsoleRoutes(mux *http.ServeMux, a *App) {
	adminJSON := func(h http.HandlerFunc) http.Handler {
		return console.AdminJSON(a.pool, a.signer, h)
	}
	mux.Handle("GET /{slug}/api/netlify/status", adminJSON(a.handleStatusJSON))
	mux.Handle("POST /{slug}/api/netlify/site", adminJSON(a.handleSitePickJSON))
	mux.Handle("POST /{slug}/api/netlify/disconnect", adminJSON(a.handleDisconnectJSON))
}

type siteOptionJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

type siteGroupJSON struct {
	Team  string           `json:"team"`
	Sites []siteOptionJSON `json:"sites"`
}

type statusJSON struct {
	NetlifyConfigured bool `json:"netlify_configured"`
	GithubConfigured  bool `json:"github_configured"`

	NetlifyConnected   bool            `json:"netlify_connected"`
	NetlifySiteName    string          `json:"netlify_site_name"`
	NetlifyRepoOwner   string          `json:"netlify_repo_owner"`
	NetlifyRepoName    string          `json:"netlify_repo_name"`
	NetlifyNeedsPicker bool            `json:"netlify_needs_picker"`
	NetlifySitesError  string          `json:"netlify_sites_error"`
	SitesByTeam        []siteGroupJSON `json:"sites_by_team"`

	GithubConnected      bool   `json:"github_connected"`
	GithubAccountLogin   string `json:"github_account_login"`
	GithubInstallationID int64  `json:"github_installation_id"`

	// Full-page action URLs the client navigates to (OAuth/install flows
	// can't be plain fetches). return_to points back at the console page.
	NetlifyConnectURL   string `json:"netlify_connect_url"`
	GithubConnectURL    string `json:"github_connect_url"`
	GithubDisconnectURL string `json:"github_disconnect_url"`
}

// buildStatus assembles the settings view-model. Extracted from the old
// server-rendered handleSettingsPage so the JSON endpoint and any future
// caller share one source of truth.
func (a *App) buildStatus(ctx context.Context, tenantSlug string, tenantID uuid.UUID) (statusJSON, error) {
	cfg, err := a.svc.GetConfig(ctx, tenantID)
	if err != nil {
		return statusJSON{}, err
	}
	consoleReturn := "/" + tenantSlug + "/web/admin/netlify"
	st := statusJSON{
		NetlifyConfigured: a.svc.HasNetlifyCredentials(),
		GithubConfigured:  a.svc.HasGitHubAppConfig(),
		NetlifyConnected:  cfg.ConnectedNetlify(),
		NetlifySiteName:   cfg.NetlifySiteName,
		NetlifyRepoOwner:  cfg.NetlifyRepoOwner,
		NetlifyRepoName:   cfg.NetlifyRepoName,
		NetlifyConnectURL: "/" + tenantSlug + "/apps/netlify/connect/netlify",
		GithubConnectURL:  "/" + tenantSlug + "/apps/github/connect?return_to=" + consoleReturn,
		GithubDisconnectURL: "/" + tenantSlug + "/apps/github/disconnect?return_to=" +
			consoleReturn,
	}

	if ghSvc := a.svc.github; ghSvc != nil {
		inst, err := ghSvc.GetInstallation(ctx, tenantID)
		if err != nil {
			slog.Warn("netlify: loading github install", "error", err)
		} else if inst != nil {
			st.GithubConnected = true
			st.GithubAccountLogin = inst.AccountLogin
			st.GithubInstallationID = inst.InstallationID
		}
	}

	// "Half-connected": tokens stored but no site picked. Surface the
	// site list so the picker can render.
	if !cfg.ConnectedNetlify() && cfg.NetlifyAccessTokenCipher != "" {
		st.NetlifyNeedsPicker = true
		accessToken, err := a.enc.Decrypt(cfg.NetlifyAccessTokenCipher)
		if err != nil {
			slog.Error("netlify: decrypting access token for picker", "error", err)
			st.NetlifySitesError = "Could not decrypt stored token. Disconnect and reconnect."
		} else {
			sites, err := listNetlifySitesAcrossAccounts(ctx, accessToken)
			if err != nil {
				slog.Warn("netlify: listing sites", "error", err)
				st.NetlifySitesError = "Could not load your Netlify sites. Try disconnecting and reconnecting."
			} else {
				for _, g := range groupSitesByTeam(sites) {
					grp := siteGroupJSON{Team: g.TeamName}
					for _, s := range g.Sites {
						grp.Sites = append(grp.Sites, siteOptionJSON{ID: s.ID, Name: s.Name, URL: s.URL})
					}
					st.SitesByTeam = append(st.SitesByTeam, grp)
				}
			}
		}
	}
	return st, nil
}

func (a *App) handleStatusJSON(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	caller := auth.CallerFromContext(r.Context())
	if tenant == nil || caller == nil {
		writeNetlifyErr(w, http.StatusInternalServerError, "tenant or caller not resolved")
		return
	}
	st, err := a.buildStatus(r.Context(), tenant.Slug, caller.TenantID)
	if err != nil {
		slog.Error("netlify: building status", "error", err)
		writeNetlifyErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeNetlifyJSON(w, http.StatusOK, st)
}

func (a *App) handleSitePickJSON(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	if caller == nil {
		writeNetlifyErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var body struct {
		SiteID string `json:"site_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.SiteID == "" {
		writeNetlifyErr(w, http.StatusBadRequest, "site_id required")
		return
	}
	cfg, err := a.svc.GetConfig(r.Context(), caller.TenantID)
	if err != nil {
		writeNetlifyErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	if cfg.NetlifyAccessTokenCipher == "" {
		writeNetlifyErr(w, http.StatusBadRequest, "connect netlify first")
		return
	}
	accessToken, err := a.enc.Decrypt(cfg.NetlifyAccessTokenCipher)
	if err != nil {
		writeNetlifyErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	site, err := getNetlifySite(r.Context(), accessToken, body.SiteID)
	if err != nil {
		writeNetlifyErr(w, http.StatusBadGateway, "could not load site")
		return
	}
	prodBranch := site.BuildSettings.Branch
	if prodBranch == "" {
		prodBranch = site.DefaultBranch
	}
	owner, repo, _ := parseRepoURL(site.BuildSettings.RepoURL)
	if err := SaveNetlifySite(r.Context(), a.pool, caller.TenantID,
		site.ID, site.Name, prodBranch, owner, repo); err != nil {
		writeNetlifyErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeNetlifyJSON(w, http.StatusOK, map[string]any{
		"message": fmt.Sprintf("Site set to %s.", site.Name),
	})
}

func (a *App) handleDisconnectJSON(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	if caller == nil {
		writeNetlifyErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := ClearNetlify(r.Context(), a.pool, caller.TenantID); err != nil {
		writeNetlifyErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeNetlifyJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeNetlifyErr(w http.ResponseWriter, status int, msg string) {
	writeNetlifyJSON(w, status, map[string]any{"error": msg})
}
