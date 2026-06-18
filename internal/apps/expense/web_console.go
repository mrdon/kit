package expense

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/console"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/services"
)

// registerExpenseRoutes wires the console JSON API. Reports are role-scoped
// (any member can read; submitter/admin edits), so these use console.JSON
// (caller required, not admin-only).
func registerExpenseRoutes(mux *http.ServeMux, a *ExpenseApp) {
	jsonRoute := func(h http.HandlerFunc) http.Handler {
		return console.JSON(a.svc.pool, a.signer, h)
	}
	mux.Handle("GET /{slug}/api/expenses", jsonRoute(a.handleList))
	mux.Handle("POST /{slug}/api/expenses", jsonRoute(a.handleCreate))
	mux.Handle("GET /{slug}/api/expenses/meta", jsonRoute(a.handleMeta))
	mux.Handle("GET /{slug}/api/expenses/policy", jsonRoute(a.handleGetPolicy))
	mux.Handle("PUT /{slug}/api/expenses/policy", jsonRoute(a.handleSetPolicy))
	mux.Handle("GET /{slug}/api/expenses/{id}", jsonRoute(a.handleGet))
	mux.Handle("POST /{slug}/api/expenses/{id}/approver", jsonRoute(a.handleAssignApprover))
	mux.Handle("POST /{slug}/api/expenses/{id}/items", jsonRoute(a.handleAddItem))
	mux.Handle("PATCH /{slug}/api/expenses/{id}/items/{itemID}", jsonRoute(a.handleUpdateItem))
	mux.Handle("DELETE /{slug}/api/expenses/{id}/items/{itemID}", jsonRoute(a.handleRemoveItem))
	mux.Handle("POST /{slug}/api/expenses/{id}/submit", jsonRoute(a.handleTransition("submit_expense_report")))
	mux.Handle("POST /{slug}/api/expenses/{id}/approve", jsonRoute(a.handleTransition("approve_expense_report")))
	mux.Handle("POST /{slug}/api/expenses/{id}/reject", jsonRoute(a.handleTransition("reject_expense_report")))
	mux.Handle("POST /{slug}/api/expenses/{id}/reimburse", jsonRoute(a.handleTransition("mark_expense_reimbursed")))
	mux.Handle("POST /{slug}/api/expenses/{id}/reopen", jsonRoute(a.handleTransition("reopen_expense_report")))
	mux.Handle("POST /{slug}/api/expenses/{id}/comments", jsonRoute(a.handleComment))
}

func (a *ExpenseApp) handleList(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	q := r.URL.Query()
	f := ReportFilters{
		Status:        q.Get("status"),
		MineOnly:      q.Get("mine_only") == "true",
		Search:        q.Get("search"),
		IncludeClosed: q.Get("include_closed") == "true",
	}
	reports, err := a.svc.List(r.Context(), caller, f)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	if reports == nil {
		reports = []Report{}
	}
	expenseJSON(w, http.StatusOK, map[string]any{"reports": reports})
}

func (a *ExpenseApp) handleCreate(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	var body struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Currency    string `json:"currency"`
		RoleScope   string `json:"role_scope"`
		Approver    string `json:"approver"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		expenseErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Title == "" {
		expenseErr(w, http.StatusBadRequest, "title is required")
		return
	}
	rep, err := a.svc.Create(r.Context(), caller, CreateInput{
		Title: body.Title, Description: body.Description, Currency: body.Currency,
		RoleName: body.RoleScope, ApproverRef: body.Approver,
	})
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	expenseJSON(w, http.StatusOK, map[string]any{"report": rep})
}

func (a *ExpenseApp) handleGet(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := pathReportID(w, r)
	if !ok {
		return
	}
	rep, events, err := a.svc.Get(r.Context(), caller, id)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	if events == nil {
		events = []ReportEvent{}
	}
	expenseJSON(w, http.StatusOK, map[string]any{
		"report":      rep,
		"events":      events,
		"can_approve": a.svc.CanApprove(r.Context(), caller, rep),
	})
}

func (a *ExpenseApp) handleGetPolicy(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	pol, err := a.svc.GetPolicy(r.Context(), caller)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	expenseJSON(w, http.StatusOK, map[string]any{"policy": pol})
}

func (a *ExpenseApp) handleSetPolicy(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	var body struct {
		ApproverRole string `json:"approver_role"`
		Approver     string `json:"approver"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		expenseErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	pol, err := a.svc.SetPolicy(r.Context(), caller, SetPolicyInput{
		ApproverRole: body.ApproverRole, ApproverRef: body.Approver,
	})
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	expenseJSON(w, http.StatusOK, map[string]any{"policy": pol})
}

func (a *ExpenseApp) handleComment(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := pathReportID(w, r)
	if !ok {
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Content == "" {
		expenseErr(w, http.StatusBadRequest, "content is required")
		return
	}
	if err := a.svc.AddComment(r.Context(), caller, id, body.Content); err != nil {
		a.serviceErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *ExpenseApp) handleAssignApprover(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := pathReportID(w, r)
	if !ok {
		return
	}
	var body struct {
		Approver string `json:"approver"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		expenseErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	rep, err := a.svc.AssignApprover(r.Context(), caller, id, body.Approver)
	if err != nil {
		a.serviceErr(w, err)
		return
	}
	expenseJSON(w, http.StatusOK, map[string]any{"report": rep})
}

// handleTransition returns a handler that runs one status transition and
// returns the updated report. reject also reads a {reason} body.
func (a *ExpenseApp) handleTransition(tool string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := auth.CallerFromContext(r.Context())
		id, ok := pathReportID(w, r)
		if !ok {
			return
		}
		var rep *Report
		var err error
		switch tool {
		case "submit_expense_report":
			rep, err = a.svc.SubmitReport(r.Context(), caller, id)
		case "approve_expense_report":
			rep, err = a.svc.ApproveReport(r.Context(), caller, id)
		case "mark_expense_reimbursed":
			rep, err = a.svc.MarkReimbursed(r.Context(), caller, id)
		case "reopen_expense_report":
			rep, err = a.svc.ReopenReport(r.Context(), caller, id)
		case "reject_expense_report":
			var body struct {
				Reason string `json:"reason"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			rep, err = a.svc.RejectReport(r.Context(), caller, id, body.Reason)
		}
		if err != nil {
			a.serviceErr(w, err)
			return
		}
		expenseJSON(w, http.StatusOK, map[string]any{"report": rep})
	}
}

// handleMeta returns the picklists the UI needs.
func (a *ExpenseApp) handleMeta(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	expenseJSON(w, http.StatusOK, map[string]any{
		"roles":      caller.Roles,
		"statuses":   []string{StatusDraft, StatusSubmitted, StatusApproved, StatusRejected, StatusReimbursed},
		"currencies": []string{"USD", "EUR", "GBP", "CAD", "AUD"},
		"is_admin":   caller.IsAdmin,
	})
}

// serviceErr maps a known domain error to status+message, else 500.
func (a *ExpenseApp) serviceErr(w http.ResponseWriter, err error) {
	if msg := expenseErrMessage(err); msg != "" {
		expenseErr(w, statusForError(err), msg)
		return
	}
	slog.Error("expense: unexpected service error", "error", err)
	expenseErr(w, http.StatusInternalServerError, "internal error")
}

func statusForError(err error) int {
	switch {
	case errors.Is(err, services.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, services.ErrForbidden), errors.Is(err, ErrSelfApproval):
		return http.StatusForbidden
	case errors.Is(err, ErrInvalidTransition), errors.Is(err, ErrNotEditable),
		errors.Is(err, ErrNoItems), errors.Is(err, ErrInvalidRole),
		errors.Is(err, ErrPrimaryRoleNotSet), errors.Is(err, ErrInvalidApprover):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func pathReportID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		expenseErr(w, http.StatusBadRequest, "invalid report id")
		return uuid.UUID{}, false
	}
	return id, true
}

func expenseJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func expenseErr(w http.ResponseWriter, status int, msg string) {
	expenseJSON(w, status, map[string]any{"error": msg})
}
