package square

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestResolveRangeDefaults(t *testing.T) {
	start, end, err := resolveRange("America/Denver", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := end.Sub(start).Hours(); got < 14*24-1 || got > 14*24+1 {
		t.Fatalf("default window = %v hours, want ~336 (14 days)", got)
	}
	// Start should be local midnight.
	if start.Hour() != 0 || start.Minute() != 0 {
		t.Fatalf("start not at midnight: %v", start)
	}
}

func TestResolveRangeExplicit(t *testing.T) {
	start, end, err := resolveRange("UTC", "2026-07-01", "2026-07-08")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if start.Format("2006-01-02") != "2026-07-01" || end.Format("2006-01-02") != "2026-07-08" {
		t.Fatalf("got %s..%s", start.Format("2006-01-02"), end.Format("2006-01-02"))
	}
}

func TestResolveRangeErrors(t *testing.T) {
	if _, _, err := resolveRange("UTC", "not-a-date", ""); err == nil {
		t.Fatal("expected error for bad start date")
	}
	if _, _, err := resolveRange("UTC", "2026-07-08", "2026-07-01"); err == nil {
		t.Fatal("expected error when end precedes start")
	}
	// Bad timezone falls back to UTC rather than erroring.
	if _, _, err := resolveRange("Not/AZone", "", ""); err != nil {
		t.Fatalf("bad tz should fall back to UTC, got %v", err)
	}
}

func TestTeamMemberDisplayName(t *testing.T) {
	cases := []struct {
		m    TeamMember
		want string
	}{
		{TeamMember{ID: "x", GivenName: "Alice", FamilyName: "Ng"}, "Alice Ng"},
		{TeamMember{ID: "x", GivenName: "Alice"}, "Alice"},
		{TeamMember{ID: "x", FamilyName: "Ng"}, "Ng"},
		{TeamMember{ID: "TM123"}, "TM123"},
	}
	for _, c := range cases {
		if got := c.m.DisplayName(); got != c.want {
			t.Errorf("DisplayName(%+v) = %q, want %q", c.m, got, c.want)
		}
	}
}

func TestFormatShiftTime(t *testing.T) {
	// A Denver shift crossing into standard formatting.
	got := formatShiftTime("2026-07-15T09:00:00-06:00", "2026-07-15T17:30:00-06:00", "America/Denver")
	if !strings.Contains(got, "09:00") || !strings.Contains(got, "17:30") {
		t.Fatalf("formatShiftTime = %q, want start/end times", got)
	}
	// Unparseable input falls back to the raw strings.
	raw := formatShiftTime("bad", "worse", "UTC")
	if raw != "bad – worse" {
		t.Fatalf("fallback = %q", raw)
	}
}

func TestFormatShiftsEmpty(t *testing.T) {
	if got := formatShifts(nil); !strings.Contains(got, "No published shifts") {
		t.Fatalf("empty format = %q", got)
	}
}

func TestAsAPIError(t *testing.T) {
	base := &APIError{StatusCode: 401, Body: "unauthorized"}
	wrapped := fmt.Errorf("refreshing: %w", base)
	var target *APIError
	if !asAPIError(wrapped, &target) || !target.IsUnauthorized() {
		t.Fatalf("asAPIError failed to unwrap 401")
	}
	if asAPIError(errors.New("plain"), &target) {
		t.Fatal("asAPIError matched a non-APIError")
	}
}
