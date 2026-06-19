package expense

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
)

// ErrIntakeDisabled is returned when an anonymous submission arrives but the
// tenant hasn't enabled the public intake page (or its owning role is gone).
var ErrIntakeDisabled = errors.New("public expense intake is not enabled")

// IntakeInput is an anonymous public-intake submission: a single receipt line
// plus the payee's email/name. There is no Kit user behind it.
type IntakeInput struct {
	Email        string
	Name         string
	Purpose      string // free text → report description
	Vendor       string
	SpentOn      *time.Time
	AmountCents  int64
	TaxCents     int64
	Category     string
	Currency     string // optional; falls back to the policy's intake currency
	AttachmentID *uuid.UUID
}

// CreateAnonymousIntake creates a one-item report from a public submission and
// moves it straight to submitted, raising the approval card through the normal
// policy path. The report has no submitter user (uuid.Nil) — only the captured
// email/name — and is owned by the policy's configured intake role so that
// role's members (and admins) can see and approve it.
func (s *ExpenseService) CreateAnonymousIntake(ctx context.Context, tenantID uuid.UUID, in IntakeInput) (*Report, error) {
	pol, err := loadPolicy(ctx, s.pool, tenantID)
	if err != nil {
		return nil, err
	}
	if !pol.IntakeEnabled || strings.TrimSpace(pol.IntakeRole) == "" {
		return nil, ErrIntakeDisabled
	}
	roleID, err := services.ResolveRoleID(ctx, s.pool, tenantID, pol.IntakeRole)
	if err != nil {
		// Owning role was deleted after intake was enabled; treat as disabled
		// rather than 500 — an admin needs to re-point it.
		return nil, ErrIntakeDisabled
	}
	scopeID, err := models.GetOrCreateScope(ctx, s.pool, tenantID, &roleID, nil)
	if err != nil {
		return nil, fmt.Errorf("resolving intake scope: %w", err)
	}

	currency := strings.ToUpper(strings.TrimSpace(in.Currency))
	if currency == "" {
		currency = pol.IntakeCurrency
	}
	if currency == "" {
		currency = "USD"
	}

	email := strings.TrimSpace(in.Email)
	name := strings.TrimSpace(in.Name)
	r := &Report{
		TenantID: tenantID,
		Title:    intakeTitle(in.Vendor, name, email),
		// SubmitterUserID stays uuid.Nil → anonymous; createReport writes NULL.
		SubmitterEmail: email,
		SubmitterName:  name,
		Description:    strings.TrimSpace(in.Purpose),
		Status:         StatusDraft,
		ScopeID:        scopeID,
		Currency:       currency,
	}
	if err := createReport(ctx, s.pool, r); err != nil {
		return nil, fmt.Errorf("creating intake report: %w", err)
	}

	it := &Item{
		TenantID:     tenantID,
		ReportID:     r.ID,
		AttachmentID: in.AttachmentID,
		Vendor:       in.Vendor,
		SpentOn:      in.SpentOn,
		AmountCents:  in.AmountCents,
		TaxCents:     in.TaxCents,
		Category:     in.Category,
	}
	if err := createItem(ctx, s.pool, it); err != nil {
		return nil, fmt.Errorf("adding intake item: %w", err)
	}
	if err := recomputeTotal(ctx, s.pool, tenantID, r.ID); err != nil {
		return nil, err
	}

	// Public submissions skip the draft-editing stage: go straight to submitted.
	// Approval routes to the intake role — there's no submitter user to fall
	// back on, and the role is exactly who the admin nominated to see and
	// approve these. (This bypasses the generic approver policy on purpose.)
	r.Status = StatusSubmitted
	r.ApproverRole = pol.IntakeRole
	if err := updateReport(ctx, s.pool, tenantID, r.ID, reportUpdate{
		Status: statusPtr(StatusSubmitted), SetSubmittedNow: true, ApproverRole: &pol.IntakeRole,
	}); err != nil {
		return nil, err
	}
	_ = appendEvent(ctx, s.pool, tenantID, r.ID, nil, "submitted",
		"Submitted via public intake ("+email+")", StatusDraft, StatusSubmitted)

	_ = s.raiseApprovalCard(ctx, tenantID, r, []Item{*it})
	return getReport(ctx, s.pool, tenantID, r.ID)
}

// intakeTitle picks a report title from the receipt vendor, falling back to the
// payee so the approval card and lists never show a blank title.
func intakeTitle(vendor, name, email string) string {
	if v := strings.TrimSpace(vendor); v != "" {
		return v
	}
	if who := strings.TrimSpace(name); who != "" {
		return "Expense from " + who
	}
	if email != "" {
		return "Expense from " + email
	}
	return "Expense submission"
}
