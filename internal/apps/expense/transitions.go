package expense

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
)

// canApprove reports whether the caller may approve/reject/reimburse a report.
// The submitter never can (no self-approval). A submitted report always carries
// a resolved approver (a specific person or a role — defaulting to admin), so
// approval is: the assigned person, or a member of the approver role, or an
// admin.
func (s *ExpenseService) canApprove(_ context.Context, c *services.Caller, r *Report) bool {
	if r.SubmitterUserID == c.UserID {
		return false
	}
	if c.IsAdmin {
		return true
	}
	if r.ApproverUserID != nil {
		return *r.ApproverUserID == c.UserID
	}
	return r.ApproverRole != "" && slices.Contains(c.Roles, r.ApproverRole)
}

// applyApprovalPolicy snapshots the tenant policy's approver onto a report at
// submit time, unless the report already carries a per-report override. Mutates
// r in place and persists the change.
func (s *ExpenseService) applyApprovalPolicy(ctx context.Context, c *services.Caller, r *Report) {
	if r.ApproverUserID != nil || r.ApproverRole != "" {
		return // explicit per-report override wins
	}
	pol, _ := loadPolicy(ctx, s.pool, c.TenantID)
	var u reportUpdate
	switch {
	case pol.ApproverUserID != nil:
		r.ApproverUserID = pol.ApproverUserID
		u.ApproverUserID = pol.ApproverUserID
	default:
		// A configured role, or the admin default when nothing is set, so every
		// submitted report has a defined approver (and visibility follows it).
		role := pol.ApproverRole
		if role == "" {
			role = models.RoleAdmin
		}
		r.ApproverRole = role
		u.ApproverRole = &role
	}
	_ = updateReport(ctx, s.pool, c.TenantID, r.ID, u)
}

// SubmitReport moves a draft report to submitted and raises the approval
// decision card (scoped to the owning role). Requires ≥1 line item.
func (s *ExpenseService) SubmitReport(ctx context.Context, c *services.Caller, reportID uuid.UUID) (*Report, error) {
	r, err := s.load(ctx, c, reportID)
	if err != nil {
		return nil, err
	}
	if !s.canWrite(c, r) {
		return nil, services.ErrForbidden
	}
	if r.Status != StatusDraft {
		return nil, fmt.Errorf("submit from %s: %w", r.Status, ErrInvalidTransition)
	}
	items, err := listItems(ctx, s.pool, c.TenantID, reportID)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, ErrNoItems
	}

	u := reportUpdate{Status: statusPtr(StatusSubmitted), SetSubmittedNow: true}
	if err := updateReport(ctx, s.pool, c.TenantID, reportID, u); err != nil {
		return nil, err
	}
	_ = appendEvent(ctx, s.pool, c.TenantID, reportID, &c.UserID, "submitted", "", StatusDraft, StatusSubmitted)

	// Snapshot the tenant approval policy onto the report, then raise the card.
	s.applyApprovalPolicy(ctx, c, r)
	if err := s.raiseApprovalCard(ctx, c, r, items); err != nil {
		// The report is already submitted; a card failure shouldn't roll that
		// back (an approver can still act via tool/console). Log-and-continue.
		return getReport(ctx, s.pool, c.TenantID, reportID)
	}
	return getReport(ctx, s.pool, c.TenantID, reportID)
}

// raiseApprovalCard creates the decision card and records its id on the
// report. No-op when no card surface is wired (tests / minimal builds).
func (s *ExpenseService) raiseApprovalCard(ctx context.Context, c *services.Caller, r *Report, items []Item) error {
	if s.app == nil || s.app.cards == nil {
		return nil
	}
	in := ApprovalDecisionInput{
		Title:    "Expense report: " + r.Title,
		Body:     approvalCardBody(r, items),
		ReportID: r.ID,
	}
	// applyApprovalPolicy has already set a specific approver or a role
	// (defaulting to admin), so route the card to whichever is present.
	switch {
	case r.ApproverUserID != nil:
		in.ApproverUserID = r.ApproverUserID
	case r.ApproverRole != "":
		in.ApproverRoleName = r.ApproverRole
	default:
		return errors.New("no approver resolved for approval card")
	}
	cardID, err := s.app.cards.CreateApprovalDecision(ctx, c.TenantID, in)
	if err != nil {
		return err
	}
	if cardID == uuid.Nil {
		return nil // no card surface created one; nothing to record
	}
	return updateReport(ctx, s.pool, c.TenantID, r.ID, reportUpdate{DecisionCardID: &cardID})
}

// ApproveReport moves a submitted report to approved. Idempotent: approving an
// already-approved report is a no-op success.
func (s *ExpenseService) ApproveReport(ctx context.Context, c *services.Caller, reportID uuid.UUID) (*Report, error) {
	r, err := s.load(ctx, c, reportID)
	if err != nil {
		return nil, err
	}
	if r.Status == StatusApproved {
		return r, nil
	}
	if !s.canApprove(ctx, c, r) {
		if r.SubmitterUserID == c.UserID {
			return nil, ErrSelfApproval
		}
		return nil, services.ErrForbidden
	}
	if r.Status != StatusSubmitted {
		return nil, fmt.Errorf("approve from %s: %w", r.Status, ErrInvalidTransition)
	}
	u := reportUpdate{Status: statusPtr(StatusApproved), DecidedByUserID: &c.UserID, SetDecidedNow: true}
	if err := updateReport(ctx, s.pool, c.TenantID, reportID, u); err != nil {
		return nil, err
	}
	_ = appendEvent(ctx, s.pool, c.TenantID, reportID, &c.UserID, "approved", "", StatusSubmitted, StatusApproved)
	return getReport(ctx, s.pool, c.TenantID, reportID)
}

// RejectReport moves a submitted report to rejected with a reason.
func (s *ExpenseService) RejectReport(ctx context.Context, c *services.Caller, reportID uuid.UUID, reason string) (*Report, error) {
	r, err := s.load(ctx, c, reportID)
	if err != nil {
		return nil, err
	}
	if r.Status == StatusRejected {
		return r, nil
	}
	if !s.canApprove(ctx, c, r) {
		if r.SubmitterUserID == c.UserID {
			return nil, ErrSelfApproval
		}
		return nil, services.ErrForbidden
	}
	if r.Status != StatusSubmitted {
		return nil, fmt.Errorf("reject from %s: %w", r.Status, ErrInvalidTransition)
	}
	u := reportUpdate{Status: statusPtr(StatusRejected), DecidedByUserID: &c.UserID, RejectionReason: &reason, SetDecidedNow: true}
	if err := updateReport(ctx, s.pool, c.TenantID, reportID, u); err != nil {
		return nil, err
	}
	_ = appendEvent(ctx, s.pool, c.TenantID, reportID, &c.UserID, "rejected", reason, StatusSubmitted, StatusRejected)
	return getReport(ctx, s.pool, c.TenantID, reportID)
}

// MarkReimbursed moves an approved report to reimbursed.
func (s *ExpenseService) MarkReimbursed(ctx context.Context, c *services.Caller, reportID uuid.UUID) (*Report, error) {
	r, err := s.load(ctx, c, reportID)
	if err != nil {
		return nil, err
	}
	if r.Status == StatusReimbursed {
		return r, nil
	}
	if !s.canApprove(ctx, c, r) {
		return nil, services.ErrForbidden
	}
	if r.Status != StatusApproved {
		return nil, fmt.Errorf("reimburse from %s: %w", r.Status, ErrInvalidTransition)
	}
	u := reportUpdate{Status: statusPtr(StatusReimbursed), SetReimbursedNow: true}
	if err := updateReport(ctx, s.pool, c.TenantID, reportID, u); err != nil {
		return nil, err
	}
	_ = appendEvent(ctx, s.pool, c.TenantID, reportID, &c.UserID, "reimbursed", "", StatusApproved, StatusReimbursed)
	return getReport(ctx, s.pool, c.TenantID, reportID)
}

// ReopenReport returns a rejected report to draft so the submitter can fix and
// resubmit it.
func (s *ExpenseService) ReopenReport(ctx context.Context, c *services.Caller, reportID uuid.UUID) (*Report, error) {
	r, err := s.load(ctx, c, reportID)
	if err != nil {
		return nil, err
	}
	if !s.canWrite(c, r) {
		return nil, services.ErrForbidden
	}
	if r.Status != StatusRejected {
		return nil, fmt.Errorf("reopen from %s: %w", r.Status, ErrInvalidTransition)
	}
	u := reportUpdate{Status: statusPtr(StatusDraft)}
	if err := updateReport(ctx, s.pool, c.TenantID, reportID, u); err != nil {
		return nil, err
	}
	_ = appendEvent(ctx, s.pool, c.TenantID, reportID, &c.UserID, "status_change", "Reopened for edits", StatusRejected, StatusDraft)
	return getReport(ctx, s.pool, c.TenantID, reportID)
}

func statusPtr(s string) *string { return &s }
