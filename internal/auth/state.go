package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// State encoding: we pack the MCP client's OAuth params into Slack's state parameter
// so we can recover them after Slack redirects back to us. The payload is signed
// with HMAC-SHA256 so an attacker can't swap the tenant slug or redirect URI
// between authorize and callback.

type oauthState struct {
	ClientID      string `json:"c"`
	RedirectURI   string `json:"r"`
	State         string `json:"s,omitempty"`
	CodeChallenge string `json:"p,omitempty"`
	TenantSlug    string `json:"t,omitempty"`
}

// deriveStateKey derives a dedicated HMAC key from a shared secret with a
// purpose prefix so compromise of the derived key does not leak the source.
func deriveStateKey(secret string) []byte {
	if strings.TrimSpace(secret) == "" {
		return nil
	}
	h := sha256.Sum256([]byte("kit-oauth-state-v1:" + secret))
	return h[:]
}

// encodeState marshals and HMAC-signs an oauthState. Output is
// `<base64url(json)>.<base64url(mac)>`.
func encodeState(key []byte, s oauthState) string {
	b, _ := json.Marshal(s)
	payload := base64.RawURLEncoding.EncodeToString(b)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	tag := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + tag
}

// decodeState verifies the HMAC tag and unmarshals the payload. Missing
// `TenantSlug` is tolerated (empty string returned) so callers can surface
// a clear error instead of panicking on stale in-flight flows.
func decodeState(key []byte, encoded string) (oauthState, error) {
	parts := strings.SplitN(encoded, ".", 2)
	if len(parts) != 2 {
		return oauthState{}, errors.New("state missing mac")
	}
	payload, gotTag := parts[0], parts[1]
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	wantTag := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(gotTag), []byte(wantTag)) {
		return oauthState{}, errors.New("state mac mismatch")
	}
	b, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return oauthState{}, fmt.Errorf("decoding base64: %w", err)
	}
	var s oauthState
	if err := json.Unmarshal(b, &s); err != nil {
		return oauthState{}, fmt.Errorf("unmarshaling state: %w", err)
	}
	return s, nil
}
