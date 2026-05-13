package github

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// httpClient is the shared HTTP client for outbound GitHub API calls.
var httpClient = &http.Client{Timeout: 10 * time.Second}

// jwtTTL is the lifespan of the app-level JWT used for App API
// endpoints. GitHub caps this at 10 minutes; we use 9 to leave a
// margin for clock skew.
const jwtTTL = 9 * time.Minute

// parsePrivateKey decodes a PEM-encoded RSA private key, accepting
// both PKCS#1 (`-----BEGIN RSA PRIVATE KEY-----`, which is what
// GitHub issues by default) and PKCS#8 (`-----BEGIN PRIVATE KEY-----`)
// envelopes.
func parsePrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("no PEM block found in private key")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	raw, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing private key: %w", err)
	}
	rsaKey, ok := raw.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("private key is not RSA")
	}
	return rsaKey, nil
}

// signAppJWT mints an RS256 JWT for the Kit GitHub App. Used to
// authenticate App-level endpoints like `/app/installations/<id>`.
// Per-installation tokens (for repo-level calls) are minted off the
// App JWT via a separate endpoint when we need them.
func signAppJWT(privateKeyPEM []byte, appID int64) (string, error) {
	key, err := parsePrivateKey(privateKeyPEM)
	if err != nil {
		return "", err
	}
	now := time.Now()
	claims := jwt.MapClaims{
		// 30s back-dated iat absorbs minor clock skew between our
		// host and GitHub's servers.
		"iat": now.Add(-30 * time.Second).Unix(),
		"exp": now.Add(jwtTTL).Unix(),
		"iss": strconv.FormatInt(appID, 10),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(key)
}

// installAccount is the subset of `GET /app/installations/<id>` we
// extract — just enough to label the install in Kit's UI.
type installAccount struct {
	Login string
	Type  string // "User" or "Organization"
}

// fetchInstallationAccount calls the GitHub App API to look up the
// owner of a given installation. Returns the slug + type so we can
// render "Installed on twdata-org" instead of "installation #12345".
func (s *Service) fetchInstallationAccount(ctx context.Context, installationID int64) (*installAccount, error) {
	if !s.HasAppConfig() {
		return nil, errors.New("kit github app not configured")
	}
	token, err := signAppJWT(s.privateKey, s.appID)
	if err != nil {
		return nil, fmt.Errorf("signing app jwt: %w", err)
	}
	url := fmt.Sprintf("https://api.github.com/app/installations/%d", installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("building install request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching install: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("github /app/installations/%d failed (status %d): %s",
			installationID, resp.StatusCode, string(body))
	}
	var payload struct {
		Account struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"account"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decoding install response: %w", err)
	}
	return &installAccount{Login: payload.Account.Login, Type: payload.Account.Type}, nil
}
