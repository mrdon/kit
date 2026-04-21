package integrations

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Signed URL capability token.
//
// Structurally mirrors internal/auth/state.go: a JSON payload + an
// HMAC-SHA256 tag, both base64url-encoded and joined with a dot.
// The payload binds the pending_integration id to its tenant and an
// absolute expiry so a leaked URL is worthless after a few minutes.
// Row-status enforcement provides single-use semantics on top of TTL —
// once the form is submitted, the same URL can't be replayed.

type tokenPayload struct {
	PendingID uuid.UUID `json:"p"`
	TenantID  uuid.UUID `json:"t"`
	ExpiresAt int64     `json:"e"` // unix seconds
}

// deriveTokenKey derives a dedicated HMAC key from a shared secret. The
// "kit-integrations-token-v1:" prefix domain-separates this key from any
// other HMAC usage that shares the same secret source.
func deriveTokenKey(secret string) []byte {
	if strings.TrimSpace(secret) == "" {
		return nil
	}
	h := sha256.Sum256([]byte("kit-integrations-token-v1:" + secret))
	return h[:]
}

// signToken encodes a payload and HMAC-signs it. Output is
// "<base64url(json)>.<base64url(mac)>" — the same shape as state.go.
func signToken(key []byte, p tokenPayload) string {
	body, _ := json.Marshal(p)
	payload := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	tag := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + tag
}

// verifyToken checks the HMAC tag and parses the payload. Returns an
// error if the tag mismatches, the payload is malformed, or the token
// has expired.
func verifyToken(key []byte, encoded string) (tokenPayload, error) {
	parts := strings.SplitN(encoded, ".", 2)
	if len(parts) != 2 {
		return tokenPayload{}, errors.New("token missing mac")
	}
	payload, gotTag := parts[0], parts[1]
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	wantTag := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(gotTag), []byte(wantTag)) {
		return tokenPayload{}, errors.New("token mac mismatch")
	}
	body, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return tokenPayload{}, fmt.Errorf("decoding base64: %w", err)
	}
	var p tokenPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return tokenPayload{}, fmt.Errorf("unmarshaling token: %w", err)
	}
	if time.Now().Unix() > p.ExpiresAt {
		return tokenPayload{}, errors.New("token expired")
	}
	return p, nil
}
