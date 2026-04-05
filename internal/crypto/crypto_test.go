package crypto

import (
	"encoding/hex"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := hex.EncodeToString(make([]byte, 32))
	// Use a deterministic key for testing
	key = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	enc, err := NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}

	plaintext := "xoxb-fake-bot-token-1234567890"
	ciphertext, err := enc.Encrypt(plaintext)
	if err != nil {
		t.Fatal(err)
	}

	if ciphertext == plaintext {
		t.Error("ciphertext should differ from plaintext")
	}

	decrypted, err := enc.Decrypt(ciphertext)
	if err != nil {
		t.Fatal(err)
	}

	if decrypted != plaintext {
		t.Errorf("decrypted = %q, want %q", decrypted, plaintext)
	}
}

func TestEncryptProducesDifferentCiphertexts(t *testing.T) {
	key := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	enc, err := NewEncryptor(key)
	if err != nil {
		t.Fatal(err)
	}

	ct1, _ := enc.Encrypt("same-input")
	ct2, _ := enc.Encrypt("same-input")

	if ct1 == ct2 {
		t.Error("two encryptions of the same plaintext should produce different ciphertexts (random nonce)")
	}
}

func TestNewEncryptorBadKey(t *testing.T) {
	if _, err := NewEncryptor("tooshort"); err == nil {
		t.Error("expected error for short key")
	}
	if _, err := NewEncryptor("not-hex-at-all!!not-hex-at-all!!not-hex-at-all!!not-hex-at-all!!"); err == nil {
		t.Error("expected error for non-hex key")
	}
}
