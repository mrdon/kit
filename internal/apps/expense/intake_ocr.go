package expense

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/mrdon/kit/internal/anthropic"
)

// receiptFields is the best-effort OCR result returned to the intake form so
// the submitter sees vendor/date/amount pre-filled (and corrects them). Every
// field is a plain string in the same shape the submit endpoint re-parses;
// empty means "couldn't read it".
type receiptFields struct {
	Vendor   string `json:"vendor"`
	SpentOn  string `json:"spent_on"`
	Amount   string `json:"amount"`
	Tax      string `json:"tax"`
	Currency string `json:"currency"`
}

const receiptInstructions = `You are reading a single expense receipt. Extract these fields and reply with ONLY a JSON object, no prose:
{"vendor": "", "spent_on": "", "amount": "", "tax": "", "currency": ""}
Rules:
- vendor: the merchant/store name.
- spent_on: the purchase date as YYYY-MM-DD.
- amount: the grand total as a plain decimal, no currency symbol (e.g. "42.10").
- tax: the tax/VAT portion as a plain decimal, or "" if not shown.
- currency: the ISO 4217 code (e.g. "USD", "GBP", "EUR"), or "" if unclear.
- Use "" for anything you cannot read. Never invent values. Output the JSON object and nothing else.`

// extractReceiptFields runs the receipt image through vision OCR and parses the
// returned JSON. Best-effort: any failure (no LLM wired, non-image upload,
// unparseable response) yields an empty result and the submitter types the
// fields by hand.
func extractReceiptFields(ctx context.Context, llm *anthropic.Client, raw []byte, mime string) receiptFields {
	if llm == nil {
		return receiptFields{}
	}
	text, err := anthropic.DescribeImage(ctx, llm, raw, mime, receiptInstructions)
	if err != nil {
		slog.Warn("expense intake: receipt OCR failed", "error", err)
		return receiptFields{}
	}
	return parseReceiptJSON(text)
}

// parseReceiptJSON pulls the first {...} object out of the model's reply and
// decodes it, normalising the currency to upper case. Tolerant of stray prose
// around the JSON; returns an empty result if nothing parses.
func parseReceiptJSON(text string) receiptFields {
	start := strings.IndexByte(text, '{')
	end := strings.LastIndexByte(text, '}')
	if start < 0 || end <= start {
		return receiptFields{}
	}
	var f receiptFields
	if err := json.Unmarshal([]byte(text[start:end+1]), &f); err != nil {
		return receiptFields{}
	}
	f.Vendor = strings.TrimSpace(f.Vendor)
	f.SpentOn = strings.TrimSpace(f.SpentOn)
	f.Amount = strings.TrimSpace(f.Amount)
	f.Tax = strings.TrimSpace(f.Tax)
	f.Currency = strings.ToUpper(strings.TrimSpace(f.Currency))
	return f
}
