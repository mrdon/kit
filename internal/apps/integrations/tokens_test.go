package integrations

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	key := deriveTokenKey("a-test-secret-definitely-long-enough")
	if len(key) == 0 {
		t.Fatal("derived key is empty")
	}
	payload := tokenPayload{
		PendingID: uuid.New(),
		TenantID:  uuid.New(),
		ExpiresAt: time.Now().Add(5 * time.Minute).Unix(),
	}
	encoded := signToken(key, payload)
	got, err := verifyToken(key, encoded)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got.PendingID != payload.PendingID {
		t.Errorf("pending id mismatch")
	}
	if got.TenantID != payload.TenantID {
		t.Errorf("tenant id mismatch")
	}
	if got.ExpiresAt != payload.ExpiresAt {
		t.Errorf("expires mismatch")
	}
}

func TestVerifyRejectsTamperedMac(t *testing.T) {
	key := deriveTokenKey("secret")
	encoded := signToken(key, tokenPayload{
		PendingID: uuid.New(),
		TenantID:  uuid.New(),
		ExpiresAt: time.Now().Add(time.Minute).Unix(),
	})
	// Flip one char in the MAC portion.
	parts := strings.SplitN(encoded, ".", 2)
	tampered := parts[0] + "." + "XX" + parts[1][2:]
	if _, err := verifyToken(key, tampered); err == nil {
		t.Fatal("expected mac mismatch, got nil")
	}
}

func TestVerifyRejectsTamperedPayload(t *testing.T) {
	key := deriveTokenKey("secret")
	encoded := signToken(key, tokenPayload{
		PendingID: uuid.New(),
		TenantID:  uuid.New(),
		ExpiresAt: time.Now().Add(time.Minute).Unix(),
	})
	parts := strings.SplitN(encoded, ".", 2)
	// Re-encode with a different payload prefix to force a MAC mismatch.
	tampered := "AAAAAAAA" + parts[0][8:] + "." + parts[1]
	if _, err := verifyToken(key, tampered); err == nil {
		t.Fatal("expected mac mismatch on tampered payload, got nil")
	}
}

func TestVerifyRejectsExpired(t *testing.T) {
	key := deriveTokenKey("secret")
	encoded := signToken(key, tokenPayload{
		PendingID: uuid.New(),
		TenantID:  uuid.New(),
		ExpiresAt: time.Now().Add(-1 * time.Second).Unix(),
	})
	if _, err := verifyToken(key, encoded); err == nil {
		t.Fatal("expected expired error, got nil")
	}
}

func TestVerifyRejectsDifferentKey(t *testing.T) {
	encoded := signToken(deriveTokenKey("secret-a"), tokenPayload{
		PendingID: uuid.New(),
		TenantID:  uuid.New(),
		ExpiresAt: time.Now().Add(time.Minute).Unix(),
	})
	if _, err := verifyToken(deriveTokenKey("secret-b"), encoded); err == nil {
		t.Fatal("different-key verify should fail")
	}
}

func TestDeriveKeyEmptySecret(t *testing.T) {
	if key := deriveTokenKey(""); key != nil {
		t.Errorf("empty secret should return nil key")
	}
	if key := deriveTokenKey("   "); key != nil {
		t.Errorf("whitespace-only secret should return nil key")
	}
}
