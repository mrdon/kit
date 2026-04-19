package auth

import (
	"strings"
	"testing"
)

func TestStateRoundtrip(t *testing.T) {
	key := deriveStateKey("test-secret-test-secret-test-secret")
	in := oauthState{
		ClientID:      "cid-abc",
		RedirectURI:   "https://example.com/cb",
		State:         "original-state",
		CodeChallenge: "challenge",
		TenantSlug:    "acme",
	}
	encoded := encodeState(key, in)
	out, err := decodeState(key, encoded)
	if err != nil {
		t.Fatalf("decodeState: %v", err)
	}
	if out != in {
		t.Fatalf("roundtrip mismatch: got %+v, want %+v", out, in)
	}
}

func TestStateTamperedMACRejected(t *testing.T) {
	key := deriveStateKey("test-secret-test-secret-test-secret")
	encoded := encodeState(key, oauthState{ClientID: "c", RedirectURI: "r", TenantSlug: "acme"})
	// Flip the last char of the MAC tag.
	parts := strings.SplitN(encoded, ".", 2)
	if len(parts) != 2 {
		t.Fatalf("expected payload.mac")
	}
	tag := []byte(parts[1])
	if tag[len(tag)-1] == 'a' {
		tag[len(tag)-1] = 'b'
	} else {
		tag[len(tag)-1] = 'a'
	}
	tampered := parts[0] + "." + string(tag)
	if _, err := decodeState(key, tampered); err == nil {
		t.Fatalf("expected tampered state to be rejected")
	}
}

func TestStateDifferentKeyRejected(t *testing.T) {
	encoded := encodeState(deriveStateKey("secret-a"), oauthState{ClientID: "c", TenantSlug: "acme"})
	if _, err := decodeState(deriveStateKey("secret-b"), encoded); err == nil {
		t.Fatalf("expected decode with different key to fail")
	}
}

func TestStateMissingTenantSlugTolerated(t *testing.T) {
	// An older state shape without `t` should decode to an empty slug, not panic.
	key := deriveStateKey("test-secret")
	encoded := encodeState(key, oauthState{ClientID: "c", RedirectURI: "r"})
	out, err := decodeState(key, encoded)
	if err != nil {
		t.Fatalf("decodeState: %v", err)
	}
	if out.TenantSlug != "" {
		t.Fatalf("expected empty tenant slug, got %q", out.TenantSlug)
	}
}
