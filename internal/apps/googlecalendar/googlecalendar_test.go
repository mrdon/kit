package googlecalendar

import (
	"context"
	"encoding/json"
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

// Recurrence must survive the round trip and stay absent when unset — a
// stray "recurrence": null would turn a one-off into a malformed request.
func TestEventRecurrenceMarshalling(t *testing.T) {
	withRule, err := json.Marshal(Event{
		Summary:    "Trivia",
		Recurrence: []string{"RRULE:FREQ=WEEKLY;BYDAY=TU"},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(withRule), `"recurrence":["RRULE:FREQ=WEEKLY;BYDAY=TU"]`) {
		t.Fatalf("recurrence missing from payload: %s", withRule)
	}

	plain, err := json.Marshal(Event{Summary: "One-off"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(plain), "recurrence") {
		t.Fatalf("non-recurring event emitted a recurrence field: %s", plain)
	}

	// Clearing a rule must drop the field so a full PUT resets Google's copy.
	var got Event
	if err := json.Unmarshal(withRule, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Recurrence) != 1 || got.Recurrence[0] != "RRULE:FREQ=WEEKLY;BYDAY=TU" {
		t.Fatalf("round trip lost recurrence: %+v", got.Recurrence)
	}
}

func TestCalendarListEntryWritable(t *testing.T) {
	for role, want := range map[string]bool{
		"owner":          true,
		"writer":         true,
		"reader":         false,
		"freeBusyReader": false,
		"":               false,
	} {
		if got := (CalendarListEntry{AccessRole: role}).Writable(); got != want {
			t.Fatalf("accessRole %q: Writable() = %v, want %v", role, got, want)
		}
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
