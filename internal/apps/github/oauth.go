package github

import (
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/models"
)

// installPathPrefix is the per-tenant route prefix the github app
// exposes for the connect/disconnect flow. Other apps link to
// `/{slug}/apps/github/connect?return_to=...` to drive the install
// flow on behalf of their own settings UI.
const installPathPrefix = "/apps/github"

// callbackPath is the tenant-agnostic top-level path GitHub redirects
// to after install. Single fixed URL so we register one
// "Setup URL" with the Kit GitHub App on GitHub.
const callbackPath = "/oauth/github/callback"

// handleConnect kicks off the GitHub App install dance. Caller must
// be a tenant admin (enforced by the middleware chain). Sets a
// short-lived state cookie with the slug + return_to and redirects
// to GitHub's installation page.
func (a *App) handleConnect(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	if tenant == nil {
		http.Error(w, "tenant not resolved", http.StatusInternalServerError)
		return
	}
	if !a.svc.HasAppConfig() {
		http.Error(w, "kit github app not configured for this kit install", http.StatusServiceUnavailable)
		return
	}
	returnTo := r.URL.Query().Get("return_to")
	if !auth.IsSafeReturnTo(returnTo, tenant.Slug) {
		// Bad / cross-tenant return_to → default back to the github
		// app's own (currently nonexistent) settings page or root.
		returnTo = "/" + tenant.Slug + "/"
	}
	state, err := genOAuthState()
	if err != nil {
		slog.Error("github: generating state", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	setOAuthStateCookie(w, state, tenant.Slug, returnTo, isSecureRequest(r))

	q := url.Values{"state": {state}}
	installURL := "https://github.com/apps/" + url.PathEscape(a.svc.appSlug) +
		"/installations/new?" + q.Encode()
	http.Redirect(w, r, installURL, http.StatusSeeOther)
}

// handleCallback runs at the tenant-agnostic /oauth/github/callback
// path. Verifies state, identifies the tenant from the state cookie,
// records the installation_id, and bounces the user back to the
// return_to URL captured at connect time.
func (a *App) handleCallback(w http.ResponseWriter, r *http.Request) {
	if !a.svc.HasAppConfig() {
		http.Error(w, "kit github app not configured", http.StatusServiceUnavailable)
		return
	}
	urlState := r.URL.Query().Get("state")
	cookieState, cookieSlug, returnTo := readOAuthStateCookie(r)
	clearOAuthStateCookie(w, isSecureRequest(r))
	if urlState == "" || cookieState == "" || urlState != cookieState {
		http.Error(w, "oauth state mismatch", http.StatusBadRequest)
		return
	}
	setupAction := r.URL.Query().Get("setup_action")
	installIDStr := r.URL.Query().Get("installation_id")
	if installIDStr == "" {
		http.Error(w, "missing installation_id", http.StatusBadRequest)
		return
	}
	installationID, err := strconv.ParseInt(installIDStr, 10, 64)
	if err != nil {
		http.Error(w, "bad installation_id", http.StatusBadRequest)
		return
	}

	caller := auth.CallerFromContext(r.Context())
	if caller == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !caller.IsAdmin {
		http.Error(w, "admin only", http.StatusForbidden)
		return
	}
	tenant, err := models.GetTenantBySlug(r.Context(), a.pool, cookieSlug)
	if err != nil || tenant == nil {
		slog.Warn("github: callback slug not found", "slug", cookieSlug, "error", err)
		http.Error(w, "tenant not found", http.StatusBadRequest)
		return
	}
	if tenant.ID != caller.TenantID {
		http.Error(w, "tenant mismatch", http.StatusForbidden)
		return
	}

	// `setup_action=request` indicates the user requested an install
	// on an org they don't own and is now waiting for an admin to
	// approve. We don't record anything yet — there's no installation
	// to attach. Bounce them back with a hint.
	if setupAction == "request" {
		bounceWithMsg(w, r, returnTo, tenant.Slug, "Install requested — waiting for the GitHub org admin to approve.")
		return
	}

	installerID := caller.UserID
	if err := SaveInstallation(r.Context(), a.pool, tenant.ID,
		installationID, "", "", &installerID); err != nil {
		slog.Error("github: saving installation", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	bounceWithMsg(w, r, returnTo, tenant.Slug, "GitHub connected.")
}

// handleDisconnect drops the install row for the tenant. The user
// must remove the actual installation on GitHub to fully revoke
// access; we just forget it on the Kit side.
func (a *App) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	tenant := auth.TenantFromContext(r.Context())
	caller := auth.CallerFromContext(r.Context())
	if tenant == nil || caller == nil {
		http.Error(w, "tenant or caller not resolved", http.StatusInternalServerError)
		return
	}
	if err := DeleteInstallation(r.Context(), a.pool, caller.TenantID); err != nil {
		slog.Error("github: clearing installation", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	returnTo := r.URL.Query().Get("return_to")
	if returnTo == "" {
		returnTo = "/" + tenant.Slug + "/"
	}
	bounceWithMsg(w, r, returnTo, tenant.Slug,
		"GitHub disconnected from Kit. Remove the installation on GitHub to fully revoke access.")
}

func bounceWithMsg(w http.ResponseWriter, r *http.Request, returnTo, slug, msg string) {
	if !auth.IsSafeReturnTo(returnTo, slug) {
		returnTo = "/" + slug + "/"
	}
	if msg != "" {
		sep := "?"
		if strings.Contains(returnTo, "?") {
			sep = "&"
		}
		returnTo += sep + "msg=" + url.QueryEscape(msg)
	}
	http.Redirect(w, r, returnTo, http.StatusSeeOther)
}
