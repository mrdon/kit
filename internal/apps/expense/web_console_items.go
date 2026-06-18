package expense

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/auth"
)

// itemBody is the console JSON shape for add/update item. All fields optional
// except amount on add (validated in the handler).
type itemBody struct {
	Amount       string `json:"amount"`
	Vendor       string `json:"vendor"`
	SpentOn      string `json:"spent_on"`
	Tax          string `json:"tax"`
	Category     string `json:"category"`
	Note         string `json:"note"`
	AttachmentID string `json:"attachment_id"`
}

func (a *ExpenseApp) handleAddItem(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	reportID, ok := pathReportID(w, r)
	if !ok {
		return
	}
	var body itemBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		expenseErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	cents, err := parseCents(body.Amount)
	if err != nil {
		expenseErr(w, http.StatusBadRequest, "amount must be a decimal like 12.34")
		return
	}
	f := body.toFields()
	tax, spentOn, attID, msg := parseItemExtras(&f)
	if msg != "" {
		expenseErr(w, http.StatusBadRequest, msg)
		return
	}
	in := AddItemInput{
		Vendor: body.Vendor, AmountCents: cents, Category: body.Category, Note: body.Note,
		SpentOn: spentOn, AttachmentID: attID,
	}
	if tax != nil {
		in.TaxCents = *tax
	}
	it, err := a.svc.AddItem(r.Context(), caller, reportID, in)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	expenseJSON(w, http.StatusOK, map[string]any{"item": it})
}

func (a *ExpenseApp) handleUpdateItem(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	itemID, err := uuid.Parse(r.PathValue("itemID"))
	if err != nil {
		expenseErr(w, http.StatusBadRequest, "invalid item id")
		return
	}
	var body itemBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		expenseErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	var u UpdateItemInput
	if body.Amount != "" {
		cents, err := parseCents(body.Amount)
		if err != nil {
			expenseErr(w, http.StatusBadRequest, "amount must be a decimal like 12.34")
			return
		}
		u.AmountCents = &cents
	}
	if body.Vendor != "" {
		u.Vendor = &body.Vendor
	}
	if body.Category != "" {
		u.Category = &body.Category
	}
	if body.Note != "" {
		u.Note = &body.Note
	}
	f := body.toFields()
	tax, spentOn, attID, msg := parseItemExtras(&f)
	if msg != "" {
		expenseErr(w, http.StatusBadRequest, msg)
		return
	}
	u.TaxCents, u.SpentOn, u.AttachmentID = tax, spentOn, attID
	it, err := a.svc.UpdateItem(r.Context(), caller, itemID, u)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	expenseJSON(w, http.StatusOK, map[string]any{"item": it})
}

func (a *ExpenseApp) handleRemoveItem(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	itemID, err := uuid.Parse(r.PathValue("itemID"))
	if err != nil {
		expenseErr(w, http.StatusBadRequest, "invalid item id")
		return
	}
	if err := a.svc.RemoveItem(r.Context(), caller, itemID); err != nil {
		a.serviceErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (b itemBody) toFields() itemFields {
	return itemFields{
		Tax: b.Tax, SpentOn: b.SpentOn, AttachmentID: b.AttachmentID,
	}
}
