package expense

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/services"
)

// expenseErrMessage maps a service error to user-facing text, or returns ""
// when the error isn't one of the known domain errors (caller should wrap and
// surface it as a real failure). Shared by the agent and MCP handlers so both
// surfaces speak identically.
func expenseErrMessage(err error) string {
	switch {
	case errors.Is(err, services.ErrNotFound):
		return "Report not found."
	case errors.Is(err, services.ErrForbidden):
		return "You don't have permission to do that."
	case errors.Is(err, ErrSelfApproval):
		return "You can't approve your own report — ask someone else in the role or an admin."
	case errors.Is(err, ErrInvalidTransition):
		return "That action isn't allowed for the report's current status."
	case errors.Is(err, ErrNotEditable):
		return "This report can only be edited while it's a draft. Reopen it first if it was rejected."
	case errors.Is(err, ErrNoItems):
		return "Add at least one line item before submitting."
	case errors.Is(err, ErrInvalidRole):
		return "That role doesn't exist. Use list_roles to see available roles."
	case errors.Is(err, ErrInvalidApprover):
		return "Couldn't find that person to assign as approver. Use find_user to check the name."
	case errors.Is(err, ErrPrimaryRoleNotSet):
		return "You're in multiple roles — pass role_scope or set a primary role."
	default:
		return ""
	}
}

// itemFields is the raw JSON shape shared by add/update item inputs.
type itemFields struct {
	ReportID     string `json:"report_id"`
	ItemID       string `json:"item_id"`
	Amount       string `json:"amount"`
	Vendor       string `json:"vendor"`
	SpentOn      string `json:"spent_on"`
	Tax          string `json:"tax"`
	Category     string `json:"category"`
	Note         string `json:"note"`
	AttachmentID string `json:"attachment_id"`
}

// parseAddItem builds an AddItemInput from raw tool JSON, returning a
// user-facing error string (not error) for bad input so handlers can relay it.
func parseAddItem(raw json.RawMessage) (uuid.UUID, AddItemInput, string) {
	var f itemFields
	if err := json.Unmarshal(raw, &f); err != nil {
		return uuid.Nil, AddItemInput{}, "Could not parse input."
	}
	reportID, err := uuid.Parse(f.ReportID)
	if err != nil {
		return uuid.Nil, AddItemInput{}, "Invalid report_id UUID."
	}
	cents, err := parseCents(f.Amount)
	if err != nil {
		return uuid.Nil, AddItemInput{}, "Invalid amount — use a decimal like 12.34."
	}
	tax, spentOn, attID, msg := parseItemExtras(&f)
	if msg != "" {
		return uuid.Nil, AddItemInput{}, msg
	}
	in := AddItemInput{
		Vendor:       f.Vendor,
		AmountCents:  cents,
		Category:     f.Category,
		Note:         f.Note,
		SpentOn:      spentOn,
		AttachmentID: attID,
	}
	if tax != nil {
		in.TaxCents = *tax
	}
	return reportID, in, ""
}

// parseUpdateItem builds an UpdateItemInput; only fields present become non-nil.
func parseUpdateItem(raw json.RawMessage) (uuid.UUID, UpdateItemInput, string) {
	var f itemFields
	if err := json.Unmarshal(raw, &f); err != nil {
		return uuid.Nil, UpdateItemInput{}, "Could not parse input."
	}
	itemID, err := uuid.Parse(f.ItemID)
	if err != nil {
		return uuid.Nil, UpdateItemInput{}, "Invalid item_id UUID."
	}
	var u UpdateItemInput
	if f.Amount != "" {
		cents, err := parseCents(f.Amount)
		if err != nil {
			return uuid.Nil, UpdateItemInput{}, "Invalid amount — use a decimal like 12.34."
		}
		u.AmountCents = &cents
	}
	if f.Vendor != "" {
		u.Vendor = &f.Vendor
	}
	if f.Category != "" {
		u.Category = &f.Category
	}
	if f.Note != "" {
		u.Note = &f.Note
	}
	tax, spentOn, attID, msg := parseItemExtras(&f)
	if msg != "" {
		return uuid.Nil, UpdateItemInput{}, msg
	}
	u.TaxCents, u.SpentOn, u.AttachmentID = tax, spentOn, attID
	return itemID, u, ""
}

// parseItemExtras parses the optional tax/spent_on/attachment_id fields,
// returning nil for any that are absent. Shared by add + update so the parsing
// rules live in one place. The returned string is a user-facing error (empty
// on success).
func parseItemExtras(f *itemFields) (tax *int64, spentOn *time.Time, attID *uuid.UUID, msg string) {
	if f.Tax != "" {
		t, err := parseCents(f.Tax)
		if err != nil {
			return nil, nil, nil, "Invalid tax — use a decimal like 3.40."
		}
		tax = &t
	}
	if f.SpentOn != "" {
		d, err := time.Parse("2006-01-02", f.SpentOn)
		if err != nil {
			return nil, nil, nil, "Invalid spent_on — use YYYY-MM-DD."
		}
		spentOn = &d
	}
	if f.AttachmentID != "" {
		id, err := uuid.Parse(f.AttachmentID)
		if err != nil {
			return nil, nil, nil, "Invalid attachment_id UUID."
		}
		attID = &id
	}
	return tax, spentOn, attID, ""
}

// parseReportID extracts and validates a report_id field.
func parseReportID(raw json.RawMessage) (uuid.UUID, string) {
	var f struct {
		ReportID string `json:"report_id"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return uuid.Nil, "Could not parse input."
	}
	id, err := uuid.Parse(f.ReportID)
	if err != nil {
		return uuid.Nil, "Invalid report_id UUID."
	}
	return id, ""
}

// itemAddedLine renders the confirmation shown after add_expense_item.
func itemAddedLine(r *Report, it *Item) string {
	return fmt.Sprintf("Added %s to %q. Report total now %s.",
		strings.TrimSpace(itemSummary(it)), r.Title, formatCents(r.TotalCents, r.Currency))
}
