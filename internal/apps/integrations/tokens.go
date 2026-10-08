package integrations

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/auth/signedlink"
)

// Signed URL capability token.
//
// A signedlink envelope around the pending_integration id and its tenant,
// with an absolute expiry so a leaked URL is worthless after a few
// minutes. Row-status enforcement provides single-use semantics on top of
// TTL: once the form is submitted, the same URL can't be replayed.

const tokenPurpose = "kit-integrations-token-v2"

type tokenPayload struct {
	PendingID uuid.UUID `json:"p"`
	TenantID  uuid.UUID `json:"t"`
}

// newTokenSigner derives the setup-link signer from the shared secret.
// Returns nil on an empty secret so callers can refuse to mint.
func newTokenSigner(secret string) *signedlink.Signer {
	s, err := signedlink.New(secret, tokenPurpose)
	if err != nil {
		return nil
	}
	return s
}

// signToken encodes a payload and signs it, expiring at expiresAt.
func signToken(signer *signedlink.Signer, p tokenPayload, expiresAt time.Time) string {
	body, _ := json.Marshal(p)
	return signer.Sign(body, time.Until(expiresAt))
}

// verifyToken checks the signature and expiry and parses the payload.
func verifyToken(signer *signedlink.Signer, encoded string) (tokenPayload, error) {
	tok, err := signer.Verify(encoded)
	if err != nil {
		return tokenPayload{}, fmt.Errorf("verifying token: %w", err)
	}
	var p tokenPayload
	if err := json.Unmarshal(tok.Payload, &p); err != nil {
		return tokenPayload{}, fmt.Errorf("unmarshaling token: %w", err)
	}
	return p, nil
}
