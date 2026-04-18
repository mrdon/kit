package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SlackOpenIDConfig holds the Slack app credentials used to perform "Sign
// in with Slack". Reused by both the MCP OAuth 2.1 flow and the PWA's
// session-cookie flow.
type SlackOpenIDConfig struct {
	ClientID     string
	ClientSecret string
}

// SlackIdentity is what comes back from a successful Slack OpenID Connect
// exchange — the user's slack_user_id and slack_team_id.
type SlackIdentity struct {
	TeamID string
	UserID string
}

// SlackAuthorizeURL builds the Slack OpenID authorization URL to redirect
// the browser to. state is echoed back through the callback.
func SlackAuthorizeURL(cfg SlackOpenIDConfig, redirectURI, state string) string {
	return fmt.Sprintf(
		"https://slack.com/openid/connect/authorize?response_type=code&client_id=%s&scope=openid,profile&redirect_uri=%s&state=%s&nonce=%s",
		url.QueryEscape(cfg.ClientID),
		url.QueryEscape(redirectURI),
		url.QueryEscape(state),
		url.QueryEscape(randomString(16)),
	)
}

// ExchangeSlackCode calls Slack's openid.connect.token + userInfo endpoints
// to turn an authorization code into a verified SlackIdentity.
func ExchangeSlackCode(ctx context.Context, cfg SlackOpenIDConfig, code, redirectURI string) (*SlackIdentity, error) {
	hc := &http.Client{Timeout: 10 * time.Second}
	tokenResp, err := slackTokenExchange(ctx, hc, cfg, code, redirectURI)
	if err != nil {
		return nil, err
	}

	infoReq, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://slack.com/api/openid.connect.userInfo", nil)
	if err != nil {
		return nil, fmt.Errorf("building userinfo request: %w", err)
	}
	infoReq.Header.Set("Authorization", "Bearer "+tokenResp.AccessToken)
	infoResp, err := hc.Do(infoReq)
	if err != nil {
		return nil, fmt.Errorf("fetching userinfo: %w", err)
	}
	defer infoResp.Body.Close()

	var info struct {
		OK     bool   `json:"ok"`
		Sub    string `json:"sub"`
		TeamID string `json:"https://slack.com/team_id"`
		Error  string `json:"error"`
	}
	if err := json.NewDecoder(infoResp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decoding userinfo: %w", err)
	}
	if !info.OK {
		return nil, fmt.Errorf("slack userinfo: %s", info.Error)
	}
	if info.Sub == "" || info.TeamID == "" {
		return nil, errors.New("incomplete userinfo response")
	}
	return &SlackIdentity{TeamID: info.TeamID, UserID: info.Sub}, nil
}

func slackTokenExchange(ctx context.Context, hc *http.Client, cfg SlackOpenIDConfig, code, redirectURI string) (*slackTokenResponse, error) {
	data := url.Values{
		"client_id":     {cfg.ClientID},
		"client_secret": {cfg.ClientSecret},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"grant_type":    {"authorization_code"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://slack.com/api/openid.connect.token", strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("building token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("posting token request: %w", err)
	}
	defer resp.Body.Close()
	var out slackTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding token response: %w", err)
	}
	if !out.OK {
		return nil, fmt.Errorf("slack token exchange: %s", out.Error)
	}
	return &out, nil
}

type slackTokenResponse struct {
	OK          bool   `json:"ok"`
	Error       string `json:"error"`
	AccessToken string `json:"access_token"`
}
