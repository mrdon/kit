package expense

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/services"
)

// enableIntake turns on public intake owned/approved by the founders role.
func (f *fixture) enableIntake(t *testing.T) {
	t.Helper()
	if _, err := f.svc.SetIntakeConfig(context.Background(), f.caller(f.admin), SetIntakeInput{
		Enabled: true, Role: "founders", Currency: "USD",
	}); err != nil {
		t.Fatalf("enable intake: %v", err)
	}
}

func TestIntakeDisabledByDefault(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.CreateAnonymousIntake(context.Background(), f.tenant.ID, IntakeInput{
		Email: "vol@example.org", Vendor: "Store", AmountCents: 1200,
	})
	if !errors.Is(err, ErrIntakeDisabled) {
		t.Fatalf("expected ErrIntakeDisabled, got %v", err)
	}
}

func TestIntakeRequiresAdmin(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.SetIntakeConfig(context.Background(), f.caller(f.alice), SetIntakeInput{
		Enabled: true, Role: "founders",
	}); !errors.Is(err, services.ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}

func TestIntakeEnableNeedsRole(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.SetIntakeConfig(context.Background(), f.caller(f.admin), SetIntakeInput{
		Enabled: true, Role: "",
	}); !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("expected ErrInvalidRole, got %v", err)
	}
}

func TestIntakeHappyPath(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.enableIntake(t)

	rep, err := f.svc.CreateAnonymousIntake(ctx, f.tenant.ID, IntakeInput{
		Email: "vol@example.org", Name: "Val Volunteer",
		Vendor: "Snack Shack", AmountCents: 4250, Purpose: "event snacks",
	})
	if err != nil {
		t.Fatalf("intake: %v", err)
	}
	if rep.Status != StatusSubmitted {
		t.Fatalf("status = %s, want submitted", rep.Status)
	}
	if rep.SubmitterUserID != uuid.Nil {
		t.Fatalf("submitter should be nil for anonymous, got %s", rep.SubmitterUserID)
	}
	if rep.SubmitterEmail != "vol@example.org" || rep.SubmitterName != "Val Volunteer" {
		t.Fatalf("submitter email/name not stored: %+v", rep)
	}
	if rep.TotalCents != 4250 {
		t.Fatalf("total = %d, want 4250", rep.TotalCents)
	}
	if rep.Title != "Snack Shack" {
		t.Fatalf("title = %q, want vendor-derived", rep.Title)
	}

	// A founders member (not the submitter) can see and approve it.
	if _, _, err := f.svc.Get(ctx, f.caller(f.bob), rep.ID); err != nil {
		t.Fatalf("founders member should see intake report: %v", err)
	}
	// An unrelated member cannot.
	if _, _, err := f.svc.Get(ctx, f.caller(f.carol), rep.ID); !errors.Is(err, services.ErrNotFound) {
		t.Fatalf("non-approver should not see intake report, got %v", err)
	}
	if _, err := f.svc.ApproveReport(ctx, f.caller(f.bob), rep.ID); err != nil {
		t.Fatalf("founders approve: %v", err)
	}
}

// TestIntakePreservesApproverPolicy: saving the approver policy and the intake
// config independently must not clobber each other (they share one row).
func TestIntakePreservesApproverPolicy(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, err := f.svc.SetPolicy(ctx, f.caller(f.admin), SetPolicyInput{ApproverRole: "founders"}); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	if _, err := f.svc.SetIntakeConfig(ctx, f.caller(f.admin), SetIntakeInput{
		Enabled: true, Role: "founders", Currency: "GBP",
	}); err != nil {
		t.Fatalf("set intake: %v", err)
	}
	pol, err := f.svc.GetPolicy(ctx, f.caller(f.admin))
	if err != nil {
		t.Fatalf("get policy: %v", err)
	}
	if pol.ApproverRole != "founders" {
		t.Fatalf("approver policy lost after intake save: %q", pol.ApproverRole)
	}
	if !pol.IntakeEnabled || pol.IntakeRole != "founders" || pol.IntakeCurrency != "GBP" {
		t.Fatalf("intake config not round-tripped: %+v", pol)
	}

	// Re-saving approver policy must keep intake on.
	if _, err := f.svc.SetPolicy(ctx, f.caller(f.admin), SetPolicyInput{ApproverRole: "founders"}); err != nil {
		t.Fatalf("re-set policy: %v", err)
	}
	pol, _ = f.svc.GetPolicy(ctx, f.caller(f.admin))
	if !pol.IntakeEnabled {
		t.Fatalf("intake disabled by approver-policy save")
	}
}

func TestApprovalCardBodyShowsAnonymousSubmitter(t *testing.T) {
	r := &Report{
		Title: "Snack Shack", Currency: "USD", TotalCents: 4250,
		SubmitterEmail: "vol@example.org", SubmitterName: "Val Volunteer",
		// SubmitterUserID stays uuid.Nil → anonymous.
	}
	body := approvalCardBody(r, []Item{{Vendor: "Snack Shack", AmountCents: 4250}})
	if !strings.Contains(body, "Val Volunteer") || !strings.Contains(body, "vol@example.org") {
		t.Fatalf("card body should name the anonymous submitter:\n%s", body)
	}

	// A normal (user-submitted) report shows no public-intake line.
	r2 := &Report{Title: "Trip", Currency: "USD", SubmitterUserID: uuid.New()}
	if strings.Contains(approvalCardBody(r2, nil), "public intake") {
		t.Fatalf("non-anonymous report should not mention public intake")
	}
}
