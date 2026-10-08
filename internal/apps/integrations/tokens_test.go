package integrations

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	signer := newTokenSigner("a-test-secret-definitely-long-enough")
	if signer == nil {
		t.Fatal("signer is nil")
	}
	payload := tokenPayload{PendingID: uuid.New(), TenantID: uuid.New()}
	encoded := signToken(signer, payload, time.Now().Add(5*time.Minute))
	got, err := verifyToken(signer, encoded)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got != payload {
		t.Errorf("payload mismatch: got %+v want %+v", got, payload)
	}
}

func TestVerifyRejectsTamperedMac(t *testing.T) {
	signer := newTokenSigner("secret")
	encoded := signToken(signer, tokenPayload{PendingID: uuid.New(), TenantID: uuid.New()}, time.Now().Add(time.Minute))
	parts := strings.SplitN(encoded, ".", 2)
	tampered := parts[0] + "." + "XX" + parts[1][2:]
	if _, err := verifyToken(signer, tampered); err == nil {
		t.Fatal("expected mac mismatch, got nil")
	}
}

func TestVerifyRejectsTamperedPayload(t *testing.T) {
	signer := newTokenSigner("secret")
	encoded := signToken(signer, tokenPayload{PendingID: uuid.New(), TenantID: uuid.New()}, time.Now().Add(time.Minute))
	parts := strings.SplitN(encoded, ".", 2)
	tampered := "AAAAAAAAAAAAAAAA" + parts[0][16:] + "." + parts[1]
	if _, err := verifyToken(signer, tampered); err == nil {
		t.Fatal("expected mac mismatch on tampered payload, got nil")
	}
}

func TestVerifyRejectsExpired(t *testing.T) {
	signer := newTokenSigner("secret")
	encoded := signToken(signer, tokenPayload{PendingID: uuid.New(), TenantID: uuid.New()}, time.Now().Add(-1*time.Second))
	if _, err := verifyToken(signer, encoded); err == nil {
		t.Fatal("expected expired error, got nil")
	}
}

func TestVerifyRejectsDifferentKey(t *testing.T) {
	encoded := signToken(newTokenSigner("secret-a"), tokenPayload{PendingID: uuid.New(), TenantID: uuid.New()}, time.Now().Add(time.Minute))
	if _, err := verifyToken(newTokenSigner("secret-b"), encoded); err == nil {
		t.Fatal("different-key verify should fail")
	}
}

func TestNewTokenSignerEmptySecret(t *testing.T) {
	if newTokenSigner("") != nil {
		t.Errorf("empty secret should return nil signer")
	}
	if newTokenSigner("   ") != nil {
		t.Errorf("whitespace-only secret should return nil signer")
	}
}
