package googlecalendar

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestDeterministicIDValidAndStable(t *testing.T) {
	id1 := DeterministicID("square:SHIFT123")
	id2 := DeterministicID("square:SHIFT123")
	if id1 != id2 {
		t.Fatalf("not stable: %q != %q", id1, id2)
	}
	if DeterministicID("square:OTHER") == id1 {
		t.Fatal("distinct seeds collided")
	}
	// Google allows lowercase a-v and 0-9, length 5-1024.
	if len(id1) < 5 || len(id1) > 1024 {
		t.Fatalf("id length out of range: %d", len(id1))
	}
	const allowed = "0123456789abcdefghijklmnopqrstuv"
	for _, r := range id1 {
		if !strings.ContainsRune(allowed, r) {
			t.Fatalf("id %q contains invalid char %q", id1, r)
		}
	}
}

func TestParseServiceAccountKey(t *testing.T) {
	good := `{"type":"service_account","client_email":"sa@proj.iam.gserviceaccount.com","private_key":"-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----\n"}`
	k, err := parseServiceAccountKey(good)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if k.TokenURI != defaultTokenURI {
		t.Fatalf("token_uri default = %q", k.TokenURI)
	}
	if _, err := parseServiceAccountKey(`{"type":"service_account"}`); err == nil {
		t.Fatal("expected error for key missing client_email/private_key")
	}
	if _, err := parseServiceAccountKey(`not json`); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestAPIErrorClassification(t *testing.T) {
	conflict := &APIError{StatusCode: http.StatusConflict}
	if !conflict.IsConflict() || conflict.IsNotFound() {
		t.Fatal("409 classification wrong")
	}
	gone := &APIError{StatusCode: http.StatusGone}
	if !gone.IsNotFound() {
		t.Fatal("410 should classify as not-found")
	}
	var target *APIError
	if !asAPIError(fmt.Errorf("wrap: %w", conflict), &target) || !target.IsConflict() {
		t.Fatal("asAPIError failed to unwrap conflict")
	}
}
