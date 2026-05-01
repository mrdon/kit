package vault

import (
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
)

// validateRSAPubKey enforces RSA-2048 with e=65537 (defends against
// downgrade / e=1 / non-RSA / malformed DER attacks at registration).
func validateRSAPubKey(der []byte) error {
	if len(der) == 0 {
		return errors.New("empty public key")
	}
	pub, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return errors.New("not an RSA public key")
	}
	if rsaPub.N == nil || rsaPub.N.BitLen() != 2048 {
		return fmt.Errorf("modulus must be 2048 bits, got %d", rsaPub.N.BitLen())
	}
	if rsaPub.E != 65537 {
		return fmt.Errorf("public exponent must be 65537, got %d", rsaPub.E)
	}
	return nil
}

func validateCiphertext(ct, nonce []byte) error {
	if len(ct) == 0 {
		return errors.New("value_ciphertext required")
	}
	if len(nonce) != 12 {
		return errors.New("value_nonce must be 12 bytes (AES-GCM)")
	}
	return nil
}

func validateScopes(scopes []models.VaultEntryScope) error {
	seen := make(map[string]bool, len(scopes))
	for i, s := range scopes {
		switch s.ScopeKind {
		case "tenant":
			if s.ScopeID != nil {
				return fmt.Errorf("scope[%d]: tenant kind must not have scope_id", i)
			}
		case "user", "role":
			if s.ScopeID == nil {
				return fmt.Errorf("scope[%d]: %s kind requires scope_id", i, s.ScopeKind)
			}
		default:
			return fmt.Errorf("scope[%d]: unknown kind %q", i, s.ScopeKind)
		}
		key := scopeKey(s)
		if seen[key] {
			return fmt.Errorf("scope[%d]: duplicate %s", i, key)
		}
		seen[key] = true
	}
	return nil
}

func scopeKey(s models.VaultEntryScope) string {
	if s.ScopeID == nil {
		return s.ScopeKind + ":-"
	}
	return s.ScopeKind + ":" + s.ScopeID.String()
}

// scopeDiff compares two scope-row sets and returns the (added, removed)
// lists of (kind, id) pairs. Output is sorted by (kind, id) so audit
// rows are deterministic across runs (Go map iteration is randomized).
func scopeDiff(before, after []models.VaultEntryScope) (added, removed []ScopeRef) {
	prev := map[string]models.VaultEntryScope{}
	for _, s := range before {
		prev[scopeKey(s)] = s
	}
	now := map[string]models.VaultEntryScope{}
	for _, s := range after {
		now[scopeKey(s)] = s
	}
	for k, s := range now {
		if _, ok := prev[k]; !ok {
			added = append(added, ScopeRef{Kind: s.ScopeKind, ID: s.ScopeID})
		}
	}
	for k, s := range prev {
		if _, ok := now[k]; !ok {
			removed = append(removed, ScopeRef{Kind: s.ScopeKind, ID: s.ScopeID})
		}
	}
	sortScopeRefs(added)
	sortScopeRefs(removed)
	return added, removed
}

func sortScopeRefs(refs []ScopeRef) {
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Kind != refs[j].Kind {
			return refs[i].Kind < refs[j].Kind
		}
		var li, lj string
		if refs[i].ID != nil {
			li = refs[i].ID.String()
		}
		if refs[j].ID != nil {
			lj = refs[j].ID.String()
		}
		return li < lj
	})
}

// pubkeyFingerprint returns a Signal-style fingerprint of a public key:
// 24 hex characters in 6 four-character groups separated by spaces. The
// format is chosen for ease of out-of-band verification — short enough
// to read aloud, long enough that an attacker can't brute-force a
// collision under SHA-256.
func pubkeyFingerprint(pub []byte) string {
	if len(pub) == 0 {
		return ""
	}
	sum := sha256.Sum256(pub)
	hexStr := hex.EncodeToString(sum[:12])
	var b strings.Builder
	for i := 0; i < len(hexStr); i += 4 {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(hexStr[i : i+4])
	}
	return b.String()
}

// dummyHash returns 32 random bytes for the constant-time miss path.
// Fresh per call so the comparison can't be distinguished from a real
// auth_hash compare via any side channel — only the timing matters.
// crypto/rand failures are catastrophic for the host; panic rather than
// fall back to a deterministic buffer that an attacker could exploit.
func dummyHash() []byte {
	b := make([]byte, 32)
	if _, err := cryptorand.Read(b); err != nil {
		panic(fmt.Errorf("crypto/rand: %w", err))
	}
	return b
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// (Compile-time assertion that uuid.UUID is referenced — needed when
// callers parse fingerprints elsewhere; the import block above keeps
// validation.go self-contained.)
var _ uuid.UUID
