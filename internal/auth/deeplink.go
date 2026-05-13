package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// DeepLinkSigner mints and verifies short-lived, single-use tokens that
// authenticate a user for one specific resource (currently: a vault entry
// reveal). The signed token rides in a URL query parameter; the middleware
// that accepts it mints a normal session cookie on consumption. See plan
// wild-enchanting-dove.md for the full design.
//
// Wire form: base64url(payload).base64url(sig). Payload is compact JSON:
// {u, t, e, x, j}. Signature is HMAC-SHA256 over the raw payload bytes.
//
// The signing key is derived from the same secret as the session cookie
// (KIT_SESSION_SECRET) but with a different SHA256 purpose prefix so the
// auth-domain (cookies) and deep-link-domain (URL tokens) stay separable.
type DeepLinkSigner struct {
	key []byte
	jti *jtiStore
	now func() time.Time
}

// DeepLinkReason describes a verify-failure reason. Exposed so callers
// can route specific reasons to specific user-facing errors and audit
// rows (e.g. expired vs. consumed vs. bad_sig).
type DeepLinkReason string

const (
	DeepLinkBadSig         DeepLinkReason = "bad_sig"
	DeepLinkExpired        DeepLinkReason = "expired"
	DeepLinkConsumed       DeepLinkReason = "consumed"
	DeepLinkMalformed      DeepLinkReason = "malformed"
	DeepLinkTenantMismatch DeepLinkReason = "tenant_mismatch"
	DeepLinkEntryMismatch  DeepLinkReason = "entry_mismatch"
)

// VerifyError carries a structured failure reason.
type VerifyError struct{ Reason DeepLinkReason }

func (e *VerifyError) Error() string { return "deep-link verify: " + string(e.Reason) }

// Claims is the verified payload of a deep-link token. EntryID is opaque
// to the signer — the caller is expected to verify it matches the
// resource being accessed (this package binds it but doesn't interpret it).
type Claims struct {
	UserID   uuid.UUID
	TenantID uuid.UUID
	EntryID  uuid.UUID
	Expires  time.Time
	JTI      string
}

// rawPayload is the JSON shape on the wire. Short field names keep the
// URL tight; the longest token will still fit under typical browser /
// proxy URL length budgets.
type rawPayload struct {
	U string `json:"u"` // user uuid hex (no dashes)
	T string `json:"t"` // tenant uuid hex
	E string `json:"e"` // entry uuid hex
	X int64  `json:"x"` // exp unix seconds
	J string `json:"j"` // jti hex (16 bytes)
}

// NewDeepLinkSigner derives the signing key from the given secret and
// initialises a single-instance jti store. Returns ErrSessionMisconfigured
// if the secret is empty so a mis-wired startup fails loudly instead of
// minting tokens that anyone could forge.
func NewDeepLinkSigner(secret string) (*DeepLinkSigner, error) {
	if strings.TrimSpace(secret) == "" {
		return nil, ErrSessionMisconfigured
	}
	h := sha256.Sum256([]byte("vault-deeplink-v1:" + secret))
	return &DeepLinkSigner{
		key: h[:],
		jti: newJTIStore(10000, 5*time.Minute),
		now: time.Now,
	}, nil
}

// Sign returns a wire-format token bound to (user, tenant, entry) with
// the given TTL. A fresh random jti is generated. Callers must publish
// the token only to its intended recipient (e.g. via Slack ephemeral) —
// the token itself is bearer-secret.
func (s *DeepLinkSigner) Sign(userID, tenantID, entryID uuid.UUID, ttl time.Duration) (string, error) {
	var jti [16]byte
	if _, err := rand.Read(jti[:]); err != nil {
		return "", fmt.Errorf("deeplink jti: %w", err)
	}
	payload := rawPayload{
		U: hex.EncodeToString(userID[:]),
		T: hex.EncodeToString(tenantID[:]),
		E: hex.EncodeToString(entryID[:]),
		X: s.now().Add(ttl).Unix(),
		J: hex.EncodeToString(jti[:]),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("deeplink marshal: %w", err)
	}
	sig := s.macOf(body)
	return base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// Verify parses a wire-format token, checks the HMAC, and checks the
// expiry. It does NOT consume the jti — callers should perform any
// resource-binding checks (e.g. entry_id match) before committing the
// single-use spend.
//
// On failure, returns a *VerifyError whose Reason names the specific
// failure mode (bad_sig, expired, malformed). On success, returns claims
// and a nil error. The jti is *not* checked here — see ConsumeJTI.
func (s *DeepLinkSigner) Verify(token string) (*Claims, error) {
	claims, err := s.verifySignature(token)
	if err != nil {
		return nil, err
	}
	if s.now().After(claims.Expires) {
		return nil, &VerifyError{Reason: DeepLinkExpired}
	}
	return claims, nil
}

// VerifySignature parses + HMAC-verifies a wire-format token and returns
// the claims regardless of expiry. Used by the deep-link middleware so
// the "expired" failure path can still audit against the (verified)
// tenant. Callers MUST treat the returned claims as authentic identity
// but stale; do not mint a session off them. On bad_sig / malformed
// input, returns the same VerifyError shape as Verify.
func (s *DeepLinkSigner) VerifySignature(token string) (*Claims, error) {
	return s.verifySignature(token)
}

func (s *DeepLinkSigner) verifySignature(token string) (*Claims, error) {
	if token == "" {
		return nil, &VerifyError{Reason: DeepLinkMalformed}
	}
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return nil, &VerifyError{Reason: DeepLinkMalformed}
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, &VerifyError{Reason: DeepLinkMalformed}
	}
	gotSig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, &VerifyError{Reason: DeepLinkMalformed}
	}
	wantSig := s.macOf(body)
	if !hmac.Equal(gotSig, wantSig) {
		return nil, &VerifyError{Reason: DeepLinkBadSig}
	}
	var p rawPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, &VerifyError{Reason: DeepLinkMalformed}
	}
	userID, err := parseHexUUID(p.U)
	if err != nil {
		return nil, &VerifyError{Reason: DeepLinkMalformed}
	}
	tenantID, err := parseHexUUID(p.T)
	if err != nil {
		return nil, &VerifyError{Reason: DeepLinkMalformed}
	}
	entryID, err := parseHexUUID(p.E)
	if err != nil {
		return nil, &VerifyError{Reason: DeepLinkMalformed}
	}
	return &Claims{
		UserID:   userID,
		TenantID: tenantID,
		EntryID:  entryID,
		Expires:  time.Unix(p.X, 0),
		JTI:      p.J,
	}, nil
}

// ConsumeJTI marks the jti as used. Returns nil on first use and a
// *VerifyError{DeepLinkConsumed} on a replay attempt. Callers should
// invoke this after Verify and after any resource-binding checks have
// passed, so a tampered URL or wrong-entry click doesn't burn a valid
// token.
func (s *DeepLinkSigner) ConsumeJTI(jti string) error {
	if !s.jti.markUsed(jti, s.now()) {
		return &VerifyError{Reason: DeepLinkConsumed}
	}
	return nil
}

func (s *DeepLinkSigner) macOf(body []byte) []byte {
	mac := hmac.New(sha256.New, s.key)
	mac.Write(body)
	return mac.Sum(nil)
}

// parseHexUUID accepts the raw 32-char hex form (no dashes) that Sign
// emits. Returns an error for any other length/charset.
func parseHexUUID(s string) (uuid.UUID, error) {
	if len(s) != 32 {
		return uuid.Nil, errors.New("uuid hex: bad length")
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return uuid.Nil, err
	}
	var u uuid.UUID
	copy(u[:], b)
	return u, nil
}

// jtiStore is a fixed-capacity, TTL-aware map of consumed jtis. Single-
// instance is fine for current Dokku deployment; horizontal scale would
// move this to Postgres. Concurrent consume races are real (a double-tap
// on a slow mobile network can fire two requests within milliseconds),
// so the mutex is load-bearing, not theoretical.
type jtiStore struct {
	mu       sync.Mutex
	seen     map[string]time.Time
	capacity int
	ttl      time.Duration
}

func newJTIStore(capacity int, ttl time.Duration) *jtiStore {
	return &jtiStore{
		seen:     make(map[string]time.Time, capacity),
		capacity: capacity,
		ttl:      ttl,
	}
}

// markUsed returns true if jti was newly recorded, false if it was
// already present (replay). The sweep + cap eviction happen under the
// same lock so a flood of distinct jtis can't grow the map unbounded.
func (j *jtiStore) markUsed(jti string, now time.Time) bool {
	j.mu.Lock()
	defer j.mu.Unlock()

	if _, ok := j.seen[jti]; ok {
		return false
	}

	// Drop entries past their TTL. O(n) but n is bounded by capacity.
	cutoff := now.Add(-j.ttl)
	for k, t := range j.seen {
		if t.Before(cutoff) {
			delete(j.seen, k)
		}
	}

	// If still at capacity after sweep, evict the oldest. Cheap because
	// the capacity is small enough that a linear scan is fine.
	if len(j.seen) >= j.capacity {
		var oldestKey string
		var oldestTime time.Time
		first := true
		for k, t := range j.seen {
			if first || t.Before(oldestTime) {
				oldestKey, oldestTime = k, t
				first = false
			}
		}
		if oldestKey != "" {
			delete(j.seen, oldestKey)
		}
	}

	j.seen[jti] = now
	return true
}
