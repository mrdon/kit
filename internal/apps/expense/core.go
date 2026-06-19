package expense

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/services"
)

// The core* functions hold the surface-agnostic logic for every expense tool.
// Both the agent (internal/tools) and MCP handlers are thin shims over these,
// guaranteeing parity: identical input parsing, validation, and user-visible
// text. A returned error is a real failure (HTTP 500 / agent error); domain
// problems come back as a friendly string with a nil error.

func coreCreateReport(ctx context.Context, c *services.Caller, svc *ExpenseService, raw json.RawMessage) (string, error) {
	var inp struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Currency    string `json:"currency"`
		RoleScope   string `json:"role_scope"`
		Approver    string `json:"approver"`
	}
	if err := json.Unmarshal(raw, &inp); err != nil {
		return "Could not parse input.", nil
	}
	r, err := svc.Create(ctx, c, CreateInput{
		Title: inp.Title, Description: inp.Description, Currency: inp.Currency,
		RoleName: inp.RoleScope, ApproverRef: inp.Approver,
	})
	if err != nil {
		if msg := expenseErrMessage(err); msg != "" {
			return msg, nil
		}
		return "", fmt.Errorf("creating report: %w", err)
	}
	return fmt.Sprintf("Created draft report [%s]: %s. Add line items, then submit.", r.ID, r.Title), nil
}

func coreAddItem(ctx context.Context, c *services.Caller, svc *ExpenseService, raw json.RawMessage) (string, error) {
	reportID, in, msg := parseAddItem(raw)
	if msg != "" {
		return msg, nil
	}
	it, err := svc.AddItem(ctx, c, reportID, in)
	if err != nil {
		if m := expenseErrMessage(err); m != "" {
			return m, nil
		}
		return "", fmt.Errorf("adding item: %w", err)
	}
	r, _, err := svc.Get(ctx, c, reportID)
	if err != nil {
		return fmt.Sprintf("Added item [%s].", it.ID), nil
	}
	return itemAddedLine(r, it), nil
}

func coreUpdateItem(ctx context.Context, c *services.Caller, svc *ExpenseService, raw json.RawMessage) (string, error) {
	itemID, u, msg := parseUpdateItem(raw)
	if msg != "" {
		return msg, nil
	}
	it, err := svc.UpdateItem(ctx, c, itemID, u)
	if err != nil {
		if m := expenseErrMessage(err); m != "" {
			return m, nil
		}
		return "", fmt.Errorf("updating item: %w", err)
	}
	return fmt.Sprintf("Updated item [%s]: %s", it.ID, strings.TrimSpace(itemSummary(it))), nil
}

func coreRemoveItem(ctx context.Context, c *services.Caller, svc *ExpenseService, raw json.RawMessage) (string, error) {
	var inp struct {
		ItemID string `json:"item_id"`
	}
	if err := json.Unmarshal(raw, &inp); err != nil {
		return "Could not parse input.", nil
	}
	itemID, err := uuid.Parse(inp.ItemID)
	if err != nil {
		return "Invalid item_id UUID.", nil
	}
	if err := svc.RemoveItem(ctx, c, itemID); err != nil {
		if m := expenseErrMessage(err); m != "" {
			return m, nil
		}
		return "", fmt.Errorf("removing item: %w", err)
	}
	return "Item removed.", nil
}

func coreListReports(ctx context.Context, c *services.Caller, svc *ExpenseService, raw json.RawMessage) (string, error) {
	var inp struct {
		Status        string `json:"status"`
		MineOnly      bool   `json:"mine_only"`
		Search        string `json:"search"`
		IncludeClosed bool   `json:"include_closed"`
	}
	if err := json.Unmarshal(raw, &inp); err != nil {
		return "Could not parse input.", nil
	}
	reports, err := svc.List(ctx, c, ReportFilters{
		Status: inp.Status, MineOnly: inp.MineOnly, Search: inp.Search, IncludeClosed: inp.IncludeClosed,
	})
	if err != nil {
		return "", fmt.Errorf("listing reports: %w", err)
	}
	if len(reports) == 0 {
		return "No expense reports found.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Found %d report(s):\n\n", len(reports))
	for i := range reports {
		b.WriteString(FormatReport(&reports[i]))
		b.WriteString("\n")
	}
	return b.String(), nil
}

func coreGetReport(ctx context.Context, c *services.Caller, svc *ExpenseService, raw json.RawMessage) (string, error) {
	id, msg := parseReportID(raw)
	if msg != "" {
		return msg, nil
	}
	r, events, err := svc.Get(ctx, c, id)
	if err != nil {
		if m := expenseErrMessage(err); m != "" {
			return m, nil
		}
		return "", fmt.Errorf("getting report: %w", err)
	}
	return FormatReportDetailed(r, events), nil
}

func coreDeleteReport(ctx context.Context, c *services.Caller, svc *ExpenseService, raw json.RawMessage) (string, error) {
	id, msg := parseReportID(raw)
	if msg != "" {
		return msg, nil
	}
	if err := svc.Delete(ctx, c, id); err != nil {
		if m := expenseErrMessage(err); m != "" {
			return m, nil
		}
		return "", fmt.Errorf("deleting report: %w", err)
	}
	return "Report deleted.", nil
}

func coreAssignApprover(ctx context.Context, c *services.Caller, svc *ExpenseService, raw json.RawMessage) (string, error) {
	var inp struct {
		ReportID string `json:"report_id"`
		Approver string `json:"approver"`
	}
	if err := json.Unmarshal(raw, &inp); err != nil {
		return "Could not parse input.", nil
	}
	id, err := uuid.Parse(inp.ReportID)
	if err != nil {
		return "Invalid report_id UUID.", nil
	}
	r, err := svc.AssignApprover(ctx, c, id, inp.Approver)
	if err != nil {
		if m := expenseErrMessage(err); m != "" {
			return m, nil
		}
		return "", fmt.Errorf("assigning approver: %w", err)
	}
	if r.ApproverUserID == nil {
		return fmt.Sprintf("Cleared the approver on %q — anyone in the role can now approve.", r.Title), nil
	}
	return fmt.Sprintf("Set the approver on %q.", r.Title), nil
}

func coreAddComment(ctx context.Context, c *services.Caller, svc *ExpenseService, raw json.RawMessage) (string, error) {
	var inp struct {
		ReportID string `json:"report_id"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal(raw, &inp); err != nil {
		return "Could not parse input.", nil
	}
	id, err := uuid.Parse(inp.ReportID)
	if err != nil {
		return "Invalid report_id UUID.", nil
	}
	if err := svc.AddComment(ctx, c, id, inp.Content); err != nil {
		if m := expenseErrMessage(err); m != "" {
			return m, nil
		}
		return "", fmt.Errorf("adding comment: %w", err)
	}
	return "Comment added.", nil
}

// coreTransition handles the report_id-only state changes plus reject (which
// also reads a reason). Centralised so the five transitions share error
// mapping and confirmation rendering.
func coreTransition(ctx context.Context, c *services.Caller, svc *ExpenseService, name string, raw json.RawMessage) (string, error) {
	id, msg := parseReportID(raw)
	if msg != "" {
		return msg, nil
	}
	var (
		r   *Report
		err error
	)
	switch name {
	case "submit_expense_report":
		r, err = svc.SubmitReport(ctx, c, id)
	case "approve_expense_report":
		r, err = svc.ApproveReport(ctx, c, id)
	case "mark_expense_reimbursed":
		r, err = svc.MarkReimbursed(ctx, c, id)
	case "reopen_expense_report":
		r, err = svc.ReopenReport(ctx, c, id)
	case "reject_expense_report":
		var inp struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal(raw, &inp)
		r, err = svc.RejectReport(ctx, c, id, inp.Reason)
	default:
		return "", fmt.Errorf("unknown transition: %s", name)
	}
	if err != nil {
		if m := expenseErrMessage(err); m != "" {
			return m, nil
		}
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return fmt.Sprintf("%s — now %s.", r.Title, statusLabel(r.Status)), nil
}

// dispatchCore routes a tool name to its core function. Both surfaces use this
// so the (name → behaviour) mapping has a single definition.
func dispatchCore(ctx context.Context, c *services.Caller, svc *ExpenseService, name string, raw json.RawMessage) (string, error) {
	switch name {
	case "create_expense_report":
		return coreCreateReport(ctx, c, svc, raw)
	case "add_expense_item":
		return coreAddItem(ctx, c, svc, raw)
	case "update_expense_item":
		return coreUpdateItem(ctx, c, svc, raw)
	case "remove_expense_item":
		return coreRemoveItem(ctx, c, svc, raw)
	case "list_expense_reports":
		return coreListReports(ctx, c, svc, raw)
	case "get_expense_report":
		return coreGetReport(ctx, c, svc, raw)
	case "assign_expense_approver":
		return coreAssignApprover(ctx, c, svc, raw)
	case "delete_expense_report":
		return coreDeleteReport(ctx, c, svc, raw)
	case "add_expense_comment":
		return coreAddComment(ctx, c, svc, raw)
	case "submit_expense_report", "approve_expense_report", "mark_expense_reimbursed", "reopen_expense_report", "reject_expense_report":
		return coreTransition(ctx, c, svc, name, raw)
	default:
		return "", fmt.Errorf("unknown expense tool: %s", name)
	}
}
