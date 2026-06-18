package expense

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/services"
)

// canWrite: only the submitter or an admin may mutate a report and its items.
// Role membership grants read (canRead) but not edit — a report is personal to
// whoever spent the money.
func (s *ExpenseService) canWrite(c *services.Caller, r *Report) bool {
	return c.IsAdmin || r.SubmitterUserID == c.UserID
}

// AddItemInput is the shape callers supply for a new line item.
type AddItemInput struct {
	Vendor       string
	SpentOn      *time.Time
	AmountCents  int64
	TaxCents     int64
	Category     string
	Note         string
	AttachmentID *uuid.UUID
}

// AddItem adds a line item to a draft report (recomputing the total). Items
// are mutable only while the report is in draft.
func (s *ExpenseService) AddItem(ctx context.Context, c *services.Caller, reportID uuid.UUID, in AddItemInput) (*Item, error) {
	r, err := s.editableReport(ctx, c, reportID)
	if err != nil {
		return nil, err
	}
	it := &Item{
		TenantID:     c.TenantID,
		ReportID:     r.ID,
		AttachmentID: in.AttachmentID,
		Vendor:       in.Vendor,
		SpentOn:      in.SpentOn,
		AmountCents:  in.AmountCents,
		TaxCents:     in.TaxCents,
		Category:     in.Category,
		Note:         in.Note,
	}
	if err := createItem(ctx, s.pool, it); err != nil {
		return nil, fmt.Errorf("adding item: %w", err)
	}
	if err := recomputeTotal(ctx, s.pool, c.TenantID, r.ID); err != nil {
		return nil, err
	}
	_ = appendEvent(ctx, s.pool, c.TenantID, r.ID, &c.UserID, "item_added", itemSummary(it), "", "")

	// Auto-categorize when the caller didn't supply one. Detached goroutine
	// (request ctx may cancel); a manual category is never overwritten.
	if it.Category == "" && s.app != nil && s.app.llm != nil {
		go runItemCategorizer(s.pool, s.app.llm, c.TenantID, it.ID, it.Vendor, it.Note)
	}
	return it, nil
}

// UpdateItemInput is sparse — set only the fields to change.
type UpdateItemInput struct {
	Vendor       *string
	SpentOn      *time.Time
	AmountCents  *int64
	TaxCents     *int64
	Category     *string
	Note         *string
	AttachmentID *uuid.UUID
}

// UpdateItem edits a line item on a draft report.
func (s *ExpenseService) UpdateItem(ctx context.Context, c *services.Caller, itemID uuid.UUID, in UpdateItemInput) (*Item, error) {
	it, err := getItem(ctx, s.pool, c.TenantID, itemID)
	if err != nil {
		return nil, services.ErrNotFound
	}
	if _, err := s.editableReport(ctx, c, it.ReportID); err != nil {
		return nil, err
	}
	if err := updateItem(ctx, s.pool, c.TenantID, itemID, itemUpdate(in)); err != nil {
		return nil, err
	}
	if err := recomputeTotal(ctx, s.pool, c.TenantID, it.ReportID); err != nil {
		return nil, err
	}
	return getItem(ctx, s.pool, c.TenantID, itemID)
}

// RemoveItem deletes a line item from a draft report.
func (s *ExpenseService) RemoveItem(ctx context.Context, c *services.Caller, itemID uuid.UUID) error {
	it, err := getItem(ctx, s.pool, c.TenantID, itemID)
	if err != nil {
		return services.ErrNotFound
	}
	if _, err := s.editableReport(ctx, c, it.ReportID); err != nil {
		return err
	}
	if err := deleteItem(ctx, s.pool, c.TenantID, itemID); err != nil {
		return err
	}
	if err := recomputeTotal(ctx, s.pool, c.TenantID, it.ReportID); err != nil {
		return err
	}
	_ = appendEvent(ctx, s.pool, c.TenantID, it.ReportID, &c.UserID, "item_removed", itemSummary(it), "", "")
	return nil
}

// editableReport loads a report and asserts the caller may edit it (writable +
// status draft). Returns ErrNotEditable when the report has left draft.
func (s *ExpenseService) editableReport(ctx context.Context, c *services.Caller, reportID uuid.UUID) (*Report, error) {
	r, err := s.load(ctx, c, reportID)
	if err != nil {
		return nil, err
	}
	if !s.canWrite(c, r) {
		return nil, services.ErrForbidden
	}
	if r.Status != StatusDraft {
		return nil, ErrNotEditable
	}
	return r, nil
}
