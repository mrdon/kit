package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/crypto"
	"github.com/mrdon/kit/internal/models"
)

var oauthScopes = []string{
	"app_mentions:read",
	"chat:write",
	"channels:read",
	"channels:history",
	"groups:read",
	"groups:history",
	"im:history",
	"im:write",
	"mpim:history",
	"files:read",
	"users:read",
	"reactions:write",
}

// OAuthHandler manages the Slack OAuth install flow.
type OAuthHandler struct {
	clientID     string
	clientSecret string
	pool         *pgxpool.Pool
	encryptor    *crypto.Encryptor
	onInstall    func(ctx context.Context, tenant *models.Tenant, installerSlackID string)
}

// NewOAuthHandler creates a new OAuth handler.
func NewOAuthHandler(clientID, clientSecret string, pool *pgxpool.Pool, enc *crypto.Encryptor, onInstall func(ctx context.Context, tenant *models.Tenant, installerSlackID string)) *OAuthHandler {
	return &OAuthHandler{
		clientID:     clientID,
		clientSecret: clientSecret,
		pool:         pool,
		encryptor:    enc,
		onInstall:    onInstall,
	}
}

// HandleInstall redirects the user to Slack's OAuth authorization page.
func (h *OAuthHandler) HandleInstall(w http.ResponseWriter, r *http.Request) {
	authURL := fmt.Sprintf(
		"https://slack.com/oauth/v2/authorize?client_id=%s&scope=%s",
		url.QueryEscape(h.clientID),
		url.QueryEscape(strings.Join(oauthScopes, ",")),
	)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// HandleCallback processes the OAuth callback from Slack.
func (h *OAuthHandler) HandleCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code parameter", http.StatusBadRequest)
		return
	}

	// Exchange code for access token
	resp, err := h.exchangeCode(code)
	if err != nil {
		slog.Error("exchanging oauth code", "error", err)
		http.Error(w, "oauth exchange failed", http.StatusInternalServerError)
		return
	}

	if !resp.OK {
		slog.Error("oauth response not ok", "error", resp.Error)
		http.Error(w, "oauth failed: "+resp.Error, http.StatusInternalServerError)
		return
	}

	// Encrypt the bot token
	encryptedToken, err := h.encryptor.Encrypt(resp.AccessToken)
	if err != nil {
		slog.Error("encrypting bot token", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	ctx := r.Context()
	teamName := resp.Team.Name
	if teamName == "" {
		teamName = resp.Team.ID
	}

	// Upsert tenant
	tenant, err := models.UpsertTenant(ctx, h.pool, resp.Team.ID, teamName, encryptedToken)
	if err != nil {
		slog.Error("upserting tenant", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Create default "member" role if tenant doesn't have one yet
	if tenant.DefaultRoleID == nil {
		role, err := models.CreateRole(ctx, h.pool, tenant.ID, "member", "Default role for all team members")
		if err != nil {
			slog.Warn("creating default role", "error", err)
		} else {
			_ = models.SetDefaultRole(ctx, h.pool, tenant.ID, &role.ID)
		}
	}

	// Create admin user (the person who installed) — fetch name from Slack
	adminName := ""
	botClient := NewClient(resp.AccessToken)
	if info, err := botClient.GetUserInfo(ctx, resp.AuthedUser.ID); err == nil {
		adminName = info.DisplayName
	}
	_, err = models.GetOrCreateUser(ctx, h.pool, tenant.ID, resp.AuthedUser.ID, adminName, true)
	if err != nil {
		slog.Error("creating admin user", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	slog.Info("oauth install complete",
		"team_id", resp.Team.ID,
		"team_name", teamName,
		"installer", resp.AuthedUser.ID,
	)

	// Trigger post-install onboarding
	if h.onInstall != nil {
		go h.onInstall(context.Background(), tenant, resp.AuthedUser.ID)
	}

	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, `<html><body><h1>Kit installed!</h1><p>Check your Slack DMs to get started.</p></body></html>`)
}

type oauthResponse struct {
	OK          bool   `json:"ok"`
	Error       string `json:"error"`
	AccessToken string `json:"access_token"`
	Team        struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"team"`
	AuthedUser struct {
		ID string `json:"id"`
	} `json:"authed_user"`
}

func (h *OAuthHandler) exchangeCode(code string) (*oauthResponse, error) {
	data := url.Values{
		"client_id":     {h.clientID},
		"client_secret": {h.clientSecret},
		"code":          {code},
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.PostForm("https://slack.com/api/oauth.v2.access", data)
	if err != nil {
		return nil, fmt.Errorf("posting to slack: %w", err)
	}
	defer resp.Body.Close()

	var result oauthResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return &result, nil
}
