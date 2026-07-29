package googlecalendar

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
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

func TestOwnerPropsDistinguishesApps(t *testing.T) {
	tenant := uuid.New()
	shifts := OwnerProps("square_shifts", tenant)
	events := OwnerProps("events", tenant)

	if shifts[PropApp] != "square_shifts" || shifts[PropTenant] != tenant.String() {
		t.Fatalf("stamp = %+v", shifts)
	}
	// Two features on one calendar must not claim each other's events.
	if shifts[PropApp] == events[PropApp] {
		t.Fatal("distinct apps produced the same ownership stamp")
	}
	// Nor may one tenant's sweep see another's.
	if OwnerProps("square_shifts", uuid.New())[PropTenant] == shifts[PropTenant] {
		t.Fatal("distinct tenants produced the same ownership stamp")
	}
}

// An unfiltered list would hand a cleanup sweep the entire calendar, so an
// empty props map must be an error rather than a match-everything query.
func TestListEventsByPrivatePropertiesRejectsEmptyFilter(t *testing.T) {
	c := &Client{accessToken: "t", tokenExpiry: time.Now().Add(time.Hour)}
	if _, err := c.ListEventsByPrivateProperties(context.Background(), "cal", nil); err == nil {
		t.Fatal("expected error for empty property filter")
	}
	if _, err := c.ListEventsByPrivateProperties(context.Background(), "cal", map[string]string{}); err == nil {
		t.Fatal("expected error for empty property filter")
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
