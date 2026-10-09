package kiosk

import (
	"errors"
	"strings"
	"testing"
)

func TestButtonsCreateListDelete(t *testing.T) {
	f := newFixture(t)
	b, err := f.svc.CreateButton(f.ctx, f.tenant.ID, ButtonInput{Label: " Untappd ", URL: "https://example.com/board"})
	if err != nil {
		t.Fatalf("CreateButton: %v", err)
	}
	if b.Label != "Untappd" {
		t.Fatalf("label = %q, want trimmed Untappd", b.Label)
	}
	got, err := ListButtons(f.ctx, f.pool, f.tenant.ID)
	if err != nil || len(got) != 1 || got[0].ID != b.ID {
		t.Fatalf("ListButtons = %+v, %v; want the one button", got, err)
	}
	if err := f.svc.DeleteButton(f.ctx, f.tenant.ID, b.ID); err != nil {
		t.Fatalf("DeleteButton: %v", err)
	}
	if err := f.svc.DeleteButton(f.ctx, f.tenant.ID, b.ID); !errors.Is(err, ErrButtonNotFound) {
		t.Fatalf("second delete = %v, want ErrButtonNotFound", err)
	}
}

func TestButtonsRejectBadInput(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name string
		in   ButtonInput
		want error
	}{
		{"no label", ButtonInput{URL: "https://example.com"}, ErrLabelInvalid},
		{"long label", ButtonInput{Label: strings.Repeat("x", maxButtonLabel+1), URL: "https://example.com"}, ErrLabelInvalid},
		{"no url", ButtonInput{Label: "Board"}, ErrURLInvalid},
		{"javascript scheme", ButtonInput{Label: "Board", URL: "javascript:alert(1)"}, ErrURLInvalid},
	}
	for _, tc := range cases {
		if _, err := f.svc.CreateButton(f.ctx, f.tenant.ID, tc.in); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestButtonsAreTenantScoped(t *testing.T) {
	f := newFixture(t)
	other := newFixture(t)
	b, err := f.svc.CreateButton(f.ctx, f.tenant.ID, ButtonInput{Label: "Board", URL: "https://example.com"})
	if err != nil {
		t.Fatalf("CreateButton: %v", err)
	}
	if got, _ := ListButtons(f.ctx, f.pool, other.tenant.ID); len(got) != 0 {
		t.Fatalf("other tenant sees %d buttons, want 0", len(got))
	}
	if err := other.svc.DeleteButton(f.ctx, other.tenant.ID, b.ID); !errors.Is(err, ErrButtonNotFound) {
		t.Fatalf("cross-tenant delete = %v, want ErrButtonNotFound", err)
	}
}
