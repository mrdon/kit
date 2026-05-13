package netlify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// netlifyAPIBase is the public REST endpoint root.
const netlifyAPIBase = "https://api.netlify.com/api/v1"

// netlifyOAuthAuthorize is the user-facing authorize endpoint.
const netlifyOAuthAuthorize = "https://app.netlify.com/authorize"

// netlifyOAuthToken is the token-exchange endpoint.
const netlifyOAuthToken = "https://api.netlify.com/oauth/token"

// httpClient is the shared HTTP client for outbound Netlify calls.
// Kept short-timeout — OAuth endpoints are quick, and the site listing
// is paged.
var httpClient = &http.Client{Timeout: 15 * time.Second}

// netlifyTokenResponse mirrors the OAuth2 token exchange response from
// Netlify. expires_in is seconds until access_token expires.
type netlifyTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	CreatedAt    int64  `json:"created_at"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

// exchangeNetlifyCode trades an authorization code for an access +
// refresh token pair.
func exchangeNetlifyCode(
	ctx context.Context,
	clientID, clientSecret, code, redirectURI string,
) (*netlifyTokenResponse, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, netlifyOAuthToken,
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("building token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("posting to netlify token endpoint: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading token response: %w", err)
	}
	var out netlifyTokenResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decoding token response (status %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode >= 400 || out.Error != "" {
		return nil, fmt.Errorf("netlify token exchange failed (status %d): %s %s",
			resp.StatusCode, out.Error, out.ErrorDesc)
	}
	if out.AccessToken == "" {
		return nil, errors.New("netlify token response missing access_token")
	}
	return &out, nil
}

// NetlifySite is the subset of the Netlify Site object we surface in
// the picker. Mirrors api.netlify.com /sites response fields we need.
type NetlifySite struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	URL           string `json:"url"`
	AdminURL      string `json:"admin_url"`
	BuildSettings struct {
		RepoURL  string `json:"repo_url"`
		RepoPath string `json:"repo_path"`
		Branch   string `json:"branch"`
	} `json:"build_settings"`
	DefaultBranch string `json:"default_branch"`
}

// listNetlifySites pulls the first page of sites the user can see.
// 100 is the documented page max and is more than any small-org user
// is going to have.
func listNetlifySites(ctx context.Context, accessToken string) ([]NetlifySite, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		netlifyAPIBase+"/sites?per_page=100", nil)
	if err != nil {
		return nil, fmt.Errorf("building sites request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("listing netlify sites: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("netlify sites list failed (status %d): %s",
			resp.StatusCode, string(body))
	}
	var sites []NetlifySite
	if err := json.NewDecoder(resp.Body).Decode(&sites); err != nil {
		return nil, fmt.Errorf("decoding sites response: %w", err)
	}
	return sites, nil
}

// getNetlifySite fetches one site's full record, including build settings
// so we can extract the connected GitHub repo coordinates.
func getNetlifySite(ctx context.Context, accessToken, siteID string) (*NetlifySite, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		netlifyAPIBase+"/sites/"+url.PathEscape(siteID), nil)
	if err != nil {
		return nil, fmt.Errorf("building site request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching netlify site: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("netlify site fetch failed (status %d): %s",
			resp.StatusCode, string(body))
	}
	var site NetlifySite
	if err := json.NewDecoder(resp.Body).Decode(&site); err != nil {
		return nil, fmt.Errorf("decoding site response: %w", err)
	}
	return &site, nil
}

// parseRepoURL splits a `repo_url` from the Netlify site (e.g.
// "https://github.com/owner/repo") into owner + repo. Returns ok=false
// for unrecognized hosts so the caller can fall through to the
// "GitHub app install does its own repo pick" path.
func parseRepoURL(repoURL string) (owner, repo string, ok bool) {
	u, err := url.Parse(repoURL)
	if err != nil || u.Host != "github.com" {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return "", "", false
	}
	return parts[0], strings.TrimSuffix(parts[1], ".git"), true
}
