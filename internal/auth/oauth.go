package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
)

const (
	tokenLifetime = 90 * 24 * time.Hour // 90 days
	codeLifetime  = 10 * time.Minute
)

// OAuthServer implements the OAuth 2.1 endpoints for MCP authentication.
// Kit acts as both Authorization Server and Resource Server.
// User identity is delegated to Slack via "Sign in with Slack".
//
// The same /oauth/callback is reused by the PWA session-cookie flow:
// when the `state` parameter starts with pwaStatePrefix, HandleCallback
// issues a session cookie via the attached SessionSigner instead of a
// Kit oauth_code. This avoids registering a second redirect URI with
// Slack.
type OAuthServer struct {
	pool         *pgxpool.Pool
	baseURL      string // e.g. "https://kit.example.com"
	clientID     string // Slack app client ID
	clientSecret string // Slack app client secret
	signer       *SessionSigner
}

// pwaStatePrefix marks a state param as belonging to the PWA cookie flow.
// The MCP OAuth flow uses base64-encoded JSON states which will never
// begin with this prefix.
const pwaStatePrefix = "pwa:"

// NewOAuthServer creates a new OAuth server. signer may be nil; if set,
// PWA-prefixed callbacks mint a session cookie via it.
func NewOAuthServer(pool *pgxpool.Pool, baseURL, slackClientID, slackClientSecret string, signer *SessionSigner) *OAuthServer {
	return &OAuthServer{
		pool:         pool,
		baseURL:      baseURL,
		clientID:     slackClientID,
		clientSecret: slackClientSecret,
		signer:       signer,
	}
}

// HandleMetadata serves RFC 8414 OAuth Authorization Server Metadata.
func (s *OAuthServer) HandleMetadata(w http.ResponseWriter, _ *http.Request) {
	meta := map[string]any{
		"issuer":                                s.baseURL,
		"authorization_endpoint":                s.baseURL + "/oauth/authorize",
		"token_endpoint":                        s.baseURL + "/oauth/token",
		"registration_endpoint":                 s.baseURL + "/oauth/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_post"},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(meta)
}

// HandleAuthorize starts the OAuth flow. Redirects to Slack's "Sign in with Slack".
func (s *OAuthServer) HandleAuthorize(w http.ResponseWriter, r *http.Request) {
	clientID := r.URL.Query().Get("client_id")
	redirectURI := r.URL.Query().Get("redirect_uri")
	state := r.URL.Query().Get("state")
	codeChallenge := r.URL.Query().Get("code_challenge")

	if clientID == "" || redirectURI == "" {
		http.Error(w, "missing client_id or redirect_uri", http.StatusBadRequest)
		return
	}

	// Verify client is registered
	client, err := models.GetOAuthClient(r.Context(), s.pool, clientID)
	if err != nil {
		slog.Error("looking up oauth client", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if client == nil {
		http.Error(w, "unknown client_id", http.StatusBadRequest)
		return
	}

	// Store the MCP client's request in a Slack OAuth state param so we can
	// recover it after Slack redirects back to us.
	slackState := encodeState(clientID, redirectURI, state, codeChallenge)

	slackURL := fmt.Sprintf(
		"https://slack.com/openid/connect/authorize?response_type=code&client_id=%s&scope=openid,profile&redirect_uri=%s&state=%s&nonce=%s",
		url.QueryEscape(s.clientID),
		url.QueryEscape(s.baseURL+"/oauth/callback"),
		url.QueryEscape(slackState),
		url.QueryEscape(randomString(16)),
	)
	http.Redirect(w, r, slackURL, http.StatusFound)
}

// HandleCallback receives the redirect from Slack after user authorizes.
// Two modes, chosen by the `state` shape:
//   - PWA session-cookie flow when state starts with pwaStatePrefix
//   - MCP OAuth 2.1 code flow otherwise
func (s *OAuthServer) HandleCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	stateParam := r.URL.Query().Get("state")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	if nonce, ok := strings.CutPrefix(stateParam, pwaStatePrefix); ok {
		s.handlePWACallback(w, r, code, nonce)
		return
	}

	// Decode the original MCP client request from state
	clientID, redirectURI, originalState, codeChallenge, err := decodeState(stateParam)
	if err != nil {
		slog.Error("decoding state", "error", err)
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}

	// Exchange Slack code for user identity
	slackUser, err := s.exchangeSlackCode(code)
	if err != nil {
		slog.Error("slack code exchange", "error", err)
		http.Error(w, "slack authentication failed", http.StatusInternalServerError)
		return
	}

	ctx := r.Context()

	// Resolve tenant + user from Slack identity
	tenant, err := models.GetTenantBySlackTeamID(ctx, s.pool, slackUser.TeamID)
	if err != nil || tenant == nil {
		slog.Error("tenant not found for team", "team_id", slackUser.TeamID, "error", err)
		http.Error(w, "organization not found — install Kit into your Slack workspace first", http.StatusNotFound)
		return
	}

	user, err := models.GetUserBySlackID(ctx, s.pool, tenant.ID, slackUser.UserID)
	if err != nil || user == nil {
		slog.Error("user not found", "slack_user_id", slackUser.UserID, "error", err)
		http.Error(w, "user not found — message Kit in Slack first to create your account", http.StatusNotFound)
		return
	}

	// Generate a Kit authorization code
	kitCode := randomString(32)
	if err := models.CreateOAuthCode(ctx, s.pool, kitCode, clientID, tenant.ID, user.ID, redirectURI, codeChallenge, time.Now().Add(codeLifetime)); err != nil {
		slog.Error("creating oauth code", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Redirect back to the MCP client with our authorization code
	u, _ := url.Parse(redirectURI)
	q := u.Query()
	q.Set("code", kitCode)
	if originalState != "" {
		q.Set("state", originalState)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

// handlePWACallback is the session-cookie branch of HandleCallback —
// exchanges the Slack code, looks up the Kit user, and issues a session
// cookie scoped to the resolved workspace slug. The caller has already
// stripped "pwa:" from the state param; what remains is the nonce that
// must match the __Host-kit_pwa_oauth cookie the browser received at
// /{slug}/login.
//
// The redirect target is derived from the tenant the Slack team_id
// resolves to, not from any caller-supplied string. If the user signed
// into a different Slack workspace than the /{slug}/login they started
// from, they silently land on the correct workspace's URL.
func (s *OAuthServer) handlePWACallback(w http.ResponseWriter, r *http.Request, code, nonce string) {
	if s.signer == nil {
		http.Error(w, "session signer not configured", http.StatusInternalServerError)
		return
	}
	if err := VerifyAndClearPWAOAuthNonce(w, r, nonce); err != nil {
		slog.Warn("pwa oauth nonce mismatch", "err", err)
		http.Error(w, "invalid oauth state", http.StatusForbidden)
		return
	}
	ident, err := ExchangeSlackCode(r.Context(), SlackOpenIDConfig{ClientID: s.clientID, ClientSecret: s.clientSecret}, code, s.baseURL+"/oauth/callback")
	if err != nil {
		slog.Error("pwa slack exchange", "error", err)
		http.Error(w, "slack authentication failed", http.StatusBadGateway)
		return
	}
	tenant, err := models.GetTenantBySlackTeamID(r.Context(), s.pool, ident.TeamID)
	if err != nil || tenant == nil {
		http.Error(w, "organization not found — install Kit into your Slack workspace first", http.StatusNotFound)
		return
	}
	user, err := models.GetUserBySlackID(r.Context(), s.pool, tenant.ID, ident.UserID)
	if err != nil || user == nil {
		http.Error(w, "user not found — message Kit in Slack first to create your account", http.StatusNotFound)
		return
	}
	if !models.IsValidSlug(tenant.Slug) {
		slog.Error("tenant has invalid slug — refusing redirect", "tenant_id", tenant.ID, "slug", tenant.Slug)
		http.Error(w, "tenant misconfigured", http.StatusInternalServerError)
		return
	}
	cookiePath := "/" + tenant.Slug + "/"
	if err := s.signer.Issue(r.Context(), w, s.pool, tenant.ID, user.ID, cookiePath); err != nil {
		slog.Error("issuing pwa session", "error", err)
		http.Error(w, "issuing session", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, cookiePath, http.StatusSeeOther)
}

// HandleToken exchanges an authorization code for an access token.
func (s *OAuthServer) HandleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	grantType := r.FormValue("grant_type")
	if grantType != "authorization_code" {
		jsonError(w, "unsupported_grant_type", http.StatusBadRequest)
		return
	}

	code := r.FormValue("code")
	codeVerifier := r.FormValue("code_verifier")

	oauthCode, err := models.ConsumeOAuthCode(r.Context(), s.pool, code)
	if err != nil {
		slog.Error("consuming oauth code", "error", err)
		jsonError(w, "server_error", http.StatusInternalServerError)
		return
	}
	if oauthCode == nil {
		jsonError(w, "invalid_grant", http.StatusBadRequest)
		return
	}

	// Verify PKCE
	if oauthCode.CodeChallenge != "" {
		if codeVerifier == "" {
			jsonError(w, "invalid_grant", http.StatusBadRequest)
			return
		}
		h := sha256.Sum256([]byte(codeVerifier))
		expected := base64.RawURLEncoding.EncodeToString(h[:])
		if expected != oauthCode.CodeChallenge {
			jsonError(w, "invalid_grant", http.StatusBadRequest)
			return
		}
	}

	// Generate and store API token
	token, tokenHash, err := models.GenerateToken()
	if err != nil {
		slog.Error("generating token", "error", err)
		jsonError(w, "server_error", http.StatusInternalServerError)
		return
	}

	if err := models.CreateAPIToken(r.Context(), s.pool, oauthCode.TenantID, oauthCode.UserID, tokenHash, time.Now().Add(tokenLifetime)); err != nil {
		slog.Error("storing token", "error", err)
		jsonError(w, "server_error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_in":   int(tokenLifetime.Seconds()),
	})
}

type slackUserInfo struct {
	TeamID string
	UserID string
}

func (s *OAuthServer) exchangeSlackCode(code string) (*slackUserInfo, error) {
	data := url.Values{
		"client_id":     {s.clientID},
		"client_secret": {s.clientSecret},
		"code":          {code},
		"redirect_uri":  {s.baseURL + "/oauth/callback"},
		"grant_type":    {"authorization_code"},
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.PostForm("https://slack.com/api/openid.connect.token", data)
	if err != nil {
		return nil, fmt.Errorf("posting to slack: %w", err)
	}
	defer resp.Body.Close()

	var tokenResp struct {
		OK          bool   `json:"ok"`
		Error       string `json:"error"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("decoding token response: %w", err)
	}
	if !tokenResp.OK {
		return nil, fmt.Errorf("slack token exchange: %s", tokenResp.Error)
	}

	// Get user info using the access token
	req, err := http.NewRequest(http.MethodGet, "https://slack.com/api/openid.connect.userInfo", nil)
	if err != nil {
		return nil, fmt.Errorf("creating userinfo request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tokenResp.AccessToken)

	infoResp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("getting user info: %w", err)
	}
	defer infoResp.Body.Close()

	var info struct {
		OK     bool   `json:"ok"`
		Sub    string `json:"sub"`                       // Slack user ID
		TeamID string `json:"https://slack.com/team_id"` // Slack team ID
		Error  string `json:"error"`
	}
	if err := json.NewDecoder(infoResp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decoding userinfo: %w", err)
	}
	if !info.OK {
		return nil, fmt.Errorf("slack userinfo: %s", info.Error)
	}
	if info.Sub == "" || info.TeamID == "" {
		return nil, errors.New("incomplete userinfo response: missing sub or team_id")
	}

	return &slackUserInfo{TeamID: info.TeamID, UserID: info.Sub}, nil
}

func jsonError(w http.ResponseWriter, errCode string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": errCode})
}

func randomString(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
