package expense

import "github.com/mrdon/kit/internal/services"

// expenseTools is the single source of tool metadata shared by the agent and
// MCP surfaces (parity). Each surface wires its own thin handler over the same
// ExpenseService methods.
var expenseTools = []services.ToolMeta{
	{
		Name:        "create_expense_report",
		Description: "Create a new draft expense report. A report groups receipts (line items) and routes for approval. It belongs to a role (the team that owns the spend); pass role_scope or rely on your primary role.",
		Schema: services.PropsReq(map[string]any{
			"title":       services.Field("string", "Short title (e.g. 'Client dinner — June')"),
			"description": services.Field("string", "Optional notes about the report"),
			"currency":    services.Field("string", "ISO currency code (default USD)"),
			"role_scope":  services.Field("string", "Role that owns the spend. Required unless you have a single or primary role."),
			"approver":    services.Field("string", "Optional person to approve this report (UUID, Slack ID, or name). If omitted, anyone in the role can approve. Use find_user if unsure."),
		}, "title"),
	},
	{
		Name:        "assign_expense_approver",
		Description: "Set or change who must approve a draft report. Pass an empty approver to clear it (then anyone in the owning role can approve).",
		Schema: services.PropsReq(map[string]any{
			"report_id": services.Field("string", "The report UUID"),
			"approver":  services.Field("string", "Person to approve (UUID, Slack ID, or name). Empty to clear."),
		}, "report_id"),
	},
	{
		Name:        "add_expense_item",
		Description: "Add a line item (one receipt) to a draft report. Amounts are decimal like '12.34'. Attach a receipt by passing attachment_id (read it first with read_attachment to fill vendor/date/amount).",
		Schema: services.PropsReq(map[string]any{
			"report_id":     services.Field("string", "The report UUID"),
			"amount":        services.Field("string", "Amount spent, decimal (e.g. '42.50')"),
			"vendor":        services.Field("string", "Merchant/vendor name"),
			"spent_on":      services.Field("string", "Date of the expense (YYYY-MM-DD)"),
			"tax":           services.Field("string", "Tax portion, decimal (e.g. '3.40')"),
			"category":      services.Field("string", "Spend category (e.g. meals, travel, supplies)"),
			"note":          services.Field("string", "Optional note"),
			"attachment_id": services.Field("string", "UUID of the receipt attachment, if any"),
		}, "report_id", "amount"),
	},
	{
		Name:        "update_expense_item",
		Description: "Edit a line item on a draft report. Only set the fields you want to change.",
		Schema: services.PropsReq(map[string]any{
			"item_id":       services.Field("string", "The item UUID"),
			"amount":        services.Field("string", "New amount, decimal"),
			"vendor":        services.Field("string", "New vendor"),
			"spent_on":      services.Field("string", "New date (YYYY-MM-DD)"),
			"tax":           services.Field("string", "New tax, decimal"),
			"category":      services.Field("string", "New category"),
			"note":          services.Field("string", "New note"),
			"attachment_id": services.Field("string", "Attach/replace receipt by UUID"),
		}, "item_id"),
	},
	{
		Name:        "remove_expense_item",
		Description: "Remove a line item from a draft report.",
		Schema: services.PropsReq(map[string]any{
			"item_id": services.Field("string", "The item UUID"),
		}, "item_id"),
	},
	{
		Name:        "submit_expense_report",
		Description: "Submit a draft report for approval. Requires at least one line item. Raises a decision card to the owning role.",
		Schema: services.PropsReq(map[string]any{
			"report_id": services.Field("string", "The report UUID"),
		}, "report_id"),
	},
	{
		Name:        "approve_expense_report",
		Description: "Approve a submitted report. You must be an admin or a member of the owning role other than the submitter (no self-approval).",
		Schema: services.PropsReq(map[string]any{
			"report_id": services.Field("string", "The report UUID"),
		}, "report_id"),
	},
	{
		Name:        "reject_expense_report",
		Description: "Reject a submitted report with a reason. The submitter can reopen, fix, and resubmit.",
		Schema: services.PropsReq(map[string]any{
			"report_id": services.Field("string", "The report UUID"),
			"reason":    services.Field("string", "Why it's being rejected"),
		}, "report_id"),
	},
	{
		Name:        "mark_expense_reimbursed",
		Description: "Mark an approved report as reimbursed (paid out).",
		Schema: services.PropsReq(map[string]any{
			"report_id": services.Field("string", "The report UUID"),
		}, "report_id"),
	},
	{
		Name:        "reopen_expense_report",
		Description: "Return a rejected report to draft so it can be fixed and resubmitted.",
		Schema: services.PropsReq(map[string]any{
			"report_id": services.Field("string", "The report UUID"),
		}, "report_id"),
	},
	{
		Name:        "list_expense_reports",
		Description: "List expense reports visible to you. By default shows only active (draft/submitted) reports; pass status or include_closed for decided ones.",
		Schema: services.Props(map[string]any{
			"status":         services.Field("string", "Filter by status: draft, submitted, approved, rejected, reimbursed"),
			"mine_only":      map[string]any{"type": "boolean", "description": "Only reports you submitted"},
			"search":         services.Field("string", "Full-text search on title and description"),
			"include_closed": map[string]any{"type": "boolean", "description": "Include approved/rejected/reimbursed reports"},
		}),
	},
	{
		Name:        "get_expense_report",
		Description: "Get a report's full details: line items, totals, and recent activity.",
		Schema: services.PropsReq(map[string]any{
			"report_id": services.Field("string", "The report UUID"),
		}, "report_id"),
	},
	{
		Name:        "add_expense_comment",
		Description: "Add a comment to a report's activity log.",
		Schema: services.PropsReq(map[string]any{
			"report_id": services.Field("string", "The report UUID"),
			"content":   services.Field("string", "Comment text"),
		}, "report_id", "content"),
	},
}
