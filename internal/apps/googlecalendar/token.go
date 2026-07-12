package googlecalendar

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
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// calendarScope is the OAuth scope requested for the service account. Full
// calendar scope is needed for event writes plus (future) calendar/ACL ops.
const calendarScope = "https://www.googleapis.com/auth/calendar"

// defaultTokenURI is Google's OAuth2 token endpoint, used when the service
// account key omits token_uri (it normally includes it).
const defaultTokenURI = "https://oauth2.googleapis.com/token"

// serviceAccountKey is the subset of a Google service-account JSON key we
// need to mint access tokens.
type serviceAccountKey struct {
	Type        string `json:"type"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
	ProjectID   string `json:"project_id"`
}

// parseServiceAccountKey decodes and validates a service-account JSON key.
func parseServiceAccountKey(raw string) (*serviceAccountKey, error) {
	var k serviceAccountKey
	if err := json.Unmarshal([]byte(raw), &k); err != nil {
		return nil, fmt.Errorf("parsing service account key JSON: %w", err)
	}
	if k.ClientEmail == "" || k.PrivateKey == "" {
		return nil, errors.New("service account key missing client_email or private_key")
	}
	if k.TokenURI == "" {
		k.TokenURI = defaultTokenURI
	}
	return &k, nil
}

// parseRSAPrivateKey decodes a PEM RSA private key (service-account keys are
// PKCS#8 `-----BEGIN PRIVATE KEY-----`; PKCS#1 is accepted too).
func parseRSAPrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("no PEM block found in service account private_key")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	raw, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing service account private key: %w", err)
	}
	rsaKey, ok := raw.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("service account private key is not RSA")
	}
	return rsaKey, nil
}

// mintAccessToken performs the JWT-bearer grant: sign a short-lived JWT
// asserting the service account identity + scope, then exchange it for an
// OAuth access token. Returns the token and its expiry.
func mintAccessToken(ctx context.Context, key *serviceAccountKey) (token string, expiry time.Time, err error) {
	rsaKey, err := parseRSAPrivateKey(key.PrivateKey)
	if err != nil {
		return "", time.Time{}, err
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"iss":   key.ClientEmail,
		"scope": calendarScope,
		"aud":   key.TokenURI,
		"iat":   now.Add(-30 * time.Second).Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}
	assertion, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(rsaKey)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("signing service account JWT: %w", err)
	}

	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, key.TokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("building token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("requesting google access token: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("reading token response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return "", time.Time{}, fmt.Errorf("google token exchange failed (status %d): %s", resp.StatusCode, string(body))
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", time.Time{}, fmt.Errorf("decoding token response: %w", err)
	}
	if out.AccessToken == "" {
		return "", time.Time{}, errors.New("google token response missing access_token")
	}
	return out.AccessToken, now.Add(time.Duration(out.ExpiresIn) * time.Second), nil
}
