package vault

import (
	cryptorand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

// minKDFIterations is the floor we accept for PBKDF2-SHA256. The
// browser sets up with 600,000 (OWASP 2024). Without a server-side
// floor, a malicious client could ship `iterations: 1`, making any
// DB-leak offline brute force trivially fast.
const minKDFIterations = 600_000

// validateKDFParams parses the JSON blob the browser sends as
// kdf_params and refuses anything weaker than the documented v2
// crypto suite (PBKDF2-SHA256, ≥600k iters, 16-byte salt).
func validateKDFParams(raw json.RawMessage) error {
	if len(raw) == 0 {
		return errors.New("kdf_params required")
	}
	var p struct {
		Algo       string `json:"algo"`
		Iterations int    `json:"iterations"`
		Salt       string `json:"salt"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("kdf_params not valid JSON: %w", err)
	}
	if p.Algo != "pbkdf2-sha256" {
		return fmt.Errorf("kdf_params.algo must be pbkdf2-sha256, got %q", p.Algo)
	}
	if p.Iterations < minKDFIterations {
		return fmt.Errorf("kdf_params.iterations must be >= %d, got %d", minKDFIterations, p.Iterations)
	}
	salt, err := base64.StdEncoding.DecodeString(p.Salt)
	if err != nil {
		return fmt.Errorf("kdf_params.salt not valid base64: %w", err)
	}
	if len(salt) != 16 {
		return fmt.Errorf("kdf_params.salt must decode to 16 bytes, got %d", len(salt))
	}
	return nil
}

// validateWrappedVaultKey checks the shape of an AES-GCM wrapped vault
// key + nonce coming in at setup or rotate. vault_key is 32 bytes;
// AES-GCM adds a 16-byte tag, so the ciphertext is exactly 48 bytes.
// Nonce is 12 bytes for AES-GCM. Either being off means the browser
// produced garbage — bail before persisting.
func validateWrappedVaultKey(ct, nonce []byte) error {
	const wantCT = 32 + 16 // vault_key + AES-GCM tag
	if len(ct) != wantCT {
		return fmt.Errorf("wrapped_vault_key must be %d bytes (AES-GCM(32-byte key)), got %d", wantCT, len(ct))
	}
	if len(nonce) != 12 {
		return errors.New("wrapped_vault_key_nonce must be 12 bytes (AES-GCM)")
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
