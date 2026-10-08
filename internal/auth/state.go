package auth

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/mrdon/kit/internal/auth/signedlink"
)

// State encoding: we pack the MCP client's OAuth params into Slack's state parameter
// so we can recover them after Slack redirects back to us. The payload is signed
// (signedlink, purpose "kit-oauth-state-v2") so an attacker can't swap the
// tenant slug or redirect URI between authorize and callback, and expires so
// a captured state can't start a flow a day later.

const (
	oauthStatePurpose = "kit-oauth-state-v2"
	oauthStateTTL     = 10 * time.Minute
)

type oauthState struct {
	ClientID      string `json:"c"`
	RedirectURI   string `json:"r"`
	State         string `json:"s,omitempty"`
	CodeChallenge string `json:"p,omitempty"`
	TenantSlug    string `json:"t,omitempty"`
}

// newStateSigner derives the state signer from the shared secret. Returns
// nil on an empty secret so the caller can refuse to start.
func newStateSigner(secret string) *signedlink.Signer {
	s, err := signedlink.New(secret, oauthStatePurpose)
	if err != nil {
		return nil
	}
	return s
}

// encodeState marshals and signs an oauthState.
func encodeState(signer *signedlink.Signer, s oauthState) string {
	b, _ := json.Marshal(s)
	return signer.Sign(b, oauthStateTTL)
}

// decodeState verifies the signature and expiry and unmarshals the payload.
// Missing `TenantSlug` is tolerated (empty string returned) so callers can
// surface a clear error instead of panicking on stale in-flight flows.
func decodeState(signer *signedlink.Signer, encoded string) (oauthState, error) {
	tok, err := signer.Verify(encoded)
	if err != nil {
		return oauthState{}, fmt.Errorf("verifying state: %w", err)
	}
	var s oauthState
	if err := json.Unmarshal(tok.Payload, &s); err != nil {
		return oauthState{}, fmt.Errorf("unmarshaling state: %w", err)
	}
	return s, nil
}
