// Package signedlink mints and verifies HMAC-signed, optionally expiring
// tokens: the thing a session cookie, an OAuth state parameter, a vault
// deep link and an integration setup URL all are underneath.
//
// Before this package each of those carried its own copy of the same
// sha256(purpose + secret) key derivation and base64(payload).base64(mac)
// framing, with the expiry handled four different ways (or not at all).
// Having one implementation means one place for the constant-time compare,
// one place for the expiry check, and one wire format to reason about.
//
// Wire form: base64url(body).base64url(mac). The body is an 8-byte
// big-endian unix expiry (zero when the token never expires) followed by
// the caller's payload bytes. The MAC is HMAC-SHA256 over the body.
//
// Domain separation: every Signer derives its key as sha256(purpose + ":" +
// secret), so two signers sharing one secret can never verify each other's
// tokens, and a compromised derived key does not leak the secret. Bump the
// purpose string ("kit-session-cookie-v3") to invalidate every outstanding
// token of that kind at once.
package signedlink

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
	"time"
)

// Errors returned by Verify and Decode. ErrExpired is only ever returned
// for a token whose signature checked out.
var (
	ErrNoSecret     = errors.New("signedlink: empty secret")
	ErrMalformed    = errors.New("signedlink: malformed token")
	ErrBadSignature = errors.New("signedlink: bad signature")
	ErrExpired      = errors.New("signedlink: expired")
)

// Signer signs and verifies tokens for one purpose.
type Signer struct {
	key []byte
	// Now is the clock used for expiry on both Sign and Verify. Tests pin
	// it; production code leaves it alone.
	Now func() time.Time
}

// Token is a decoded, signature-verified token.
type Token struct {
	Payload []byte
	// ExpiresAt is zero when the token was signed without a TTL.
	ExpiresAt time.Time
}

// Expired reports whether the token's TTL has elapsed at now. A token
// with no expiry never expires.
func (t *Token) Expired(now time.Time) bool {
	return !t.ExpiresAt.IsZero() && now.After(t.ExpiresAt)
}

// New derives a signer from a shared secret and a purpose string. The
// secret may be any high-entropy material (ENCRYPTION_KEY is what
// production passes); the purpose must be unique per token kind.
func New(secret, purpose string) (*Signer, error) {
	if strings.TrimSpace(secret) == "" {
		return nil, ErrNoSecret
	}
	h := sha256.Sum256([]byte(purpose + ":" + secret))
	return &Signer{key: h[:], Now: time.Now}, nil
}

// Sign produces a token carrying payload. A ttl of exactly zero means the
// token never expires on its own (the caller is relying on something else,
// such as an api_tokens row, to bound its life). A negative ttl yields a
// token that is already expired, which is what a caller computing
// time.Until(deadline) for a past deadline should get.
func (s *Signer) Sign(payload []byte, ttl time.Duration) string {
	var exp int64
	if ttl != 0 {
		exp = s.Now().Add(ttl).Unix()
	}
	body := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint64(body, uint64(exp)) //nolint:gosec // unix seconds are positive
	copy(body[8:], payload)
	return base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(s.mac(body))
}

// Verify checks the signature and the expiry. The error is ErrMalformed,
// ErrBadSignature or ErrExpired; the token is nil on any error.
func (s *Signer) Verify(wire string) (*Token, error) {
	tok, err := s.Decode(wire)
	if err != nil {
		return nil, err
	}
	if tok.Expired(s.Now()) {
		return nil, ErrExpired
	}
	return tok, nil
}

// Decode checks the signature but not the expiry, so a caller can read
// an authentic-but-stale token for auditing. Anything that grants access
// must call Verify instead.
func (s *Signer) Decode(wire string) (*Token, error) {
	bodyB64, macB64, ok := strings.Cut(wire, ".")
	if !ok || bodyB64 == "" {
		return nil, ErrMalformed
	}
	body, err := base64.RawURLEncoding.DecodeString(bodyB64)
	if err != nil || len(body) < 8 {
		return nil, ErrMalformed
	}
	got, err := base64.RawURLEncoding.DecodeString(macB64)
	if err != nil {
		return nil, ErrMalformed
	}
	if !hmac.Equal(got, s.mac(body)) {
		return nil, ErrBadSignature
	}
	tok := &Token{Payload: body[8:]}
	if exp := binary.BigEndian.Uint64(body); exp != 0 {
		tok.ExpiresAt = time.Unix(int64(exp), 0) //nolint:gosec // written by Sign from a positive int64
	}
	return tok, nil
}

func (s *Signer) mac(body []byte) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write(body)
	return m.Sum(nil)
}
