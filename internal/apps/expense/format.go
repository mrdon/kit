package expense

import (
	"fmt"
	"strings"
)

// formatCents renders a minor-unit amount in its currency. USD-family
// currencies get a leading symbol; everything else trails the ISO code so we
// never imply the wrong symbol.
func formatCents(cents int64, currency string) string {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		currency = "USD"
	}
	whole := cents / 100
	frac := cents % 100
	if frac < 0 {
		frac = -frac
	}
	amount := fmt.Sprintf("%d.%02d", whole, frac)
	switch currency {
	case "USD", "CAD", "AUD", "NZD":
		return "$" + amount
	case "GBP":
		return "£" + amount
	case "EUR":
		return "€" + amount
	default:
		return amount + " " + currency
	}
}

// statusLabel renders a report status for display.
func statusLabel(status string) string {
	switch status {
	case StatusDraft:
		return "Draft"
	case StatusSubmitted:
		return "Submitted"
	case StatusApproved:
		return "Approved"
	case StatusRejected:
		return "Rejected"
	case StatusReimbursed:
		return "Reimbursed"
	default:
		return status
	}
}

// itemSummary is a one-line description of a line item for the activity log.
func itemSummary(it *Item) string {
	parts := []string{}
	if it.Vendor != "" {
		parts = append(parts, it.Vendor)
	}
	parts = append(parts, formatCents(it.AmountCents, "USD"))
	if it.SpentOn != nil {
		parts = append(parts, it.SpentOn.Format("2006-01-02"))
	}
	return strings.Join(parts, " — ")
}

// approvalCardBody renders the markdown shown on the approval decision card.
func approvalCardBody(r *Report, items []Item) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**%s** submitted for approval.\n\n", r.Title)
	if r.Description != "" {
		fmt.Fprintf(&b, "%s\n\n", r.Description)
	}
	fmt.Fprintf(&b, "**Total: %s** across %d item(s):\n", formatCents(r.TotalCents, r.Currency), len(items))
	for i := range items {
		it := &items[i]
		vendor := it.Vendor
		if vendor == "" {
			vendor = "(no vendor)"
		}
		date := ""
		if it.SpentOn != nil {
			date = " · " + it.SpentOn.Format("2006-01-02")
		}
		fmt.Fprintf(&b, "- %s — %s%s\n", vendor, formatCents(it.AmountCents, r.Currency), date)
	}
	return b.String()
}

// FormatReport renders a one-line report summary for agent/MCP output.
func FormatReport(r *Report) string {
	return fmt.Sprintf("[%s] %s — %s (%s, %d item(s))",
		r.ID, r.Title, statusLabel(r.Status), formatCents(r.TotalCents, r.Currency), len(r.Items))
}

// FormatReportDetailed renders a report with its items and recent activity.
func FormatReportDetailed(r *Report, events []ReportEvent) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] %s\nStatus: %s · Total: %s · Currency: %s\n",
		r.ID, r.Title, statusLabel(r.Status), formatCents(r.TotalCents, r.Currency), r.Currency)
	if r.Description != "" {
		fmt.Fprintf(&b, "%s\n", r.Description)
	}
	if r.RejectionReason != "" {
		fmt.Fprintf(&b, "Rejected: %s\n", r.RejectionReason)
	}
	if len(r.Items) > 0 {
		b.WriteString("\nItems:\n")
		for i := range r.Items {
			it := &r.Items[i]
			vendor := it.Vendor
			if vendor == "" {
				vendor = "(no vendor)"
			}
			fmt.Fprintf(&b, "  [%s] %s — %s", it.ID, vendor, formatCents(it.AmountCents, r.Currency))
			if it.TaxCents > 0 {
				fmt.Fprintf(&b, " (tax %s)", formatCents(it.TaxCents, r.Currency))
			}
			if it.SpentOn != nil {
				fmt.Fprintf(&b, " · %s", it.SpentOn.Format("2006-01-02"))
			}
			if it.Category != "" {
				fmt.Fprintf(&b, " · %s", it.Category)
			}
			if it.AttachmentID != nil {
				b.WriteString(" · 📎 receipt")
			}
			b.WriteString("\n")
		}
	}
	if len(events) > 0 {
		b.WriteString("\nRecent activity:\n")
		for _, e := range events {
			ts := e.CreatedAt.Format("2006-01-02 15:04")
			switch e.EventType {
			case "comment":
				fmt.Fprintf(&b, "  [%s] %s\n", ts, e.Content)
			case "status_change", "submitted", "approved", "rejected", "reimbursed":
				fmt.Fprintf(&b, "  [%s] %s → %s", ts, e.OldValue, e.NewValue)
				if e.Content != "" {
					fmt.Fprintf(&b, " (%s)", e.Content)
				}
				b.WriteString("\n")
			case "item_added":
				fmt.Fprintf(&b, "  [%s] Added: %s\n", ts, e.Content)
			case "item_removed":
				fmt.Fprintf(&b, "  [%s] Removed: %s\n", ts, e.Content)
			}
		}
	}
	return b.String()
}
