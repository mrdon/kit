package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"golang.org/x/crypto/pbkdf2"
)

// TestRecoverVaultKeyRoundTrip exercises the same crypto path the
// browser used: derive enc_key + auth_hash from a password, AES-GCM
// wrap an RSA private key, RSA-OAEP wrap a vault_key. recoverVaultKey
// must walk that back.
func TestRecoverVaultKeyRoundTrip(t *testing.T) {
	// Simulate what an old per-user setup looked like in the browser.
	password := "correct horse battery staple"
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		t.Fatal(err)
	}
	masterKey := pbkdf2.Key([]byte(password), salt, kdfIterations, keyLen, sha256.New)
	authHash, err := hkdfSHA256(masterKey, salt, []byte("kit-vault-v1-auth"), keyLen)
	if err != nil {
		t.Fatal(err)
	}
	encKey, err := hkdfSHA256(masterKey, salt, []byte("kit-vault-v1-enc"), keyLen)
	if err != nil {
		t.Fatal(err)
	}

	// Generate an RSA-2048 keypair, wrap a 32-byte vault_key with the
	// public key, AES-GCM the private key under enc_key.
	rsaPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	vaultKey := make([]byte, vaultKeyLen)
	if _, err := rand.Read(vaultKey); err != nil {
		t.Fatal(err)
	}
	wrappedVK, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, &rsaPriv.PublicKey, vaultKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(rsaPriv)
	if err != nil {
		t.Fatal(err)
	}
	privNonce := make([]byte, gcmNonceLen)
	if _, err := rand.Read(privNonce); err != nil {
		t.Fatal(err)
	}
	privCT, err := aesGcmEncrypt(encKey, privNonce, pkcs8, nil)
	if err != nil {
		t.Fatal(err)
	}

	kdfJSON, err := json.Marshal(struct {
		Algo       string `json:"algo"`
		Iterations int    `json:"iterations"`
		Salt       string `json:"salt"`
	}{Algo: kdfHash, Iterations: kdfIterations, Salt: base64.StdEncoding.EncodeToString(salt)})
	if err != nil {
		t.Fatal(err)
	}

	user := &vaultUserRow{
		kdfParams:                kdfJSON,
		authHash:                 authHash,
		userPrivateKeyCiphertext: privCT,
		userPrivateKeyNonce:      privNonce,
		wrappedVaultKey:          wrappedVK,
	}

	got, err := recoverVaultKey(password, user)
	if err != nil {
		t.Fatalf("recoverVaultKey: %v", err)
	}
	if subtle.ConstantTimeCompare(got, vaultKey) != 1 {
		t.Fatalf("recovered vault_key bytes differ from original")
	}
}

func TestRecoverVaultKeyWrongPassword(t *testing.T) {
	// Build a row with one password, try to recover with another.
	salt := make([]byte, saltLen)
	rand.Read(salt)
	masterKey := pbkdf2.Key([]byte("correct"), salt, kdfIterations, keyLen, sha256.New)
	authHash, _ := hkdfSHA256(masterKey, salt, []byte("kit-vault-v1-auth"), keyLen)

	kdfJSON, _ := json.Marshal(struct {
		Algo       string `json:"algo"`
		Iterations int    `json:"iterations"`
		Salt       string `json:"salt"`
	}{Algo: kdfHash, Iterations: kdfIterations, Salt: base64.StdEncoding.EncodeToString(salt)})

	user := &vaultUserRow{
		kdfParams: kdfJSON,
		authHash:  authHash,
	}
	_, err := recoverVaultKey("wrong", user)
	if err == nil {
		t.Fatal("expected wrong-password error")
	}
}

// TestBuildNewWrapAndRoundTrip wraps a vault_key under a new password
// and confirms a fresh derivation can unwrap it (the same flow the
// running server does after the migration tool inserts the row).
func TestBuildNewWrapAndRoundTrip(t *testing.T) {
	tenantID := uuid.New()
	vaultKey := make([]byte, vaultKeyLen)
	rand.Read(vaultKey)

	w, err := buildNewWrap("new shared password", vaultKey, tenantID)
	if err != nil {
		t.Fatal(err)
	}

	// Mimic the server's unlock path: parse kdf_params, derive enc_key
	// from the same password + salt, AES-GCM-decrypt the wrapped key
	// with AAD = tenant_id bytes.
	salt, iters, err := parseKDFParams(w.kdfParams)
	if err != nil {
		t.Fatal(err)
	}
	mk := pbkdf2.Key([]byte("new shared password"), salt, iters, keyLen, sha256.New)
	encKey, _ := hkdfSHA256(mk, salt, []byte("kit-vault-v1-enc"), keyLen)
	pt, err := aesGcmDecrypt(encKey, w.nonce, w.wrappedVaultKey, tenantID[:])
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(pt, vaultKey) {
		t.Fatal("round-trip mismatch")
	}
}

// TestBuildNewWrapAADBindsTenant proves that a row wrapped under
// tenant A can't be unwrapped using tenant B as AAD.
func TestBuildNewWrapAADBindsTenant(t *testing.T) {
	tenantA := uuid.New()
	tenantB := uuid.New()
	vaultKey := make([]byte, vaultKeyLen)
	rand.Read(vaultKey)

	w, err := buildNewWrap("pw", vaultKey, tenantA)
	if err != nil {
		t.Fatal(err)
	}
	// Try to unwrap with tenant B's bytes — should fail on auth-tag check.
	salt, iters, _ := parseKDFParams(w.kdfParams)
	mk := pbkdf2.Key([]byte("pw"), salt, iters, keyLen, sha256.New)
	encKey, _ := hkdfSHA256(mk, salt, []byte("kit-vault-v1-enc"), keyLen)
	_, err = aesGcmDecrypt(encKey, w.nonce, w.wrappedVaultKey, tenantB[:])
	if err == nil {
		t.Fatal("expected AAD mismatch error when unwrapping under wrong tenant")
	}
}
