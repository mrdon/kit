package task

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps"
	"github.com/mrdon/kit/internal/apps/console"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/services"
)

// registerTaskRoutes wires the console JSON API for tasks. Tasks are
// role-scoped (any role member can see/edit), so these use console.JSON
// (caller required, not admin-only). The shared parse/error-map helpers
// (console_shared.go) keep wording identical to the MCP surface.
func registerTaskRoutes(mux apps.Mux, a *TaskApp) {
	jsonRoute := func(h http.HandlerFunc) http.Handler {
		return console.JSON(a.svc.pool, a.signer, h)
	}
	mux.Handle("GET /{slug}/api/tasks", jsonRoute(a.handleList))
	mux.Handle("POST /{slug}/api/tasks", jsonRoute(a.handleCreate))
	mux.Handle("GET /{slug}/api/tasks/meta", jsonRoute(a.handleMeta))
	mux.Handle("GET /{slug}/api/tasks/{id}", jsonRoute(a.handleGet))
	mux.Handle("PATCH /{slug}/api/tasks/{id}", jsonRoute(a.handleUpdate))
	mux.Handle("POST /{slug}/api/tasks/categorize", jsonRoute(a.handleCategorize))
	mux.Handle("POST /{slug}/api/tasks/{id}/complete", jsonRoute(a.handleComplete))
	mux.Handle("POST /{slug}/api/tasks/{id}/snooze", jsonRoute(a.handleSnooze))
	mux.Handle("POST /{slug}/api/tasks/{id}/comments", jsonRoute(a.handleComment))
}

func (a *TaskApp) handleList(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	q := r.URL.Query()
	f := TaskFilters{
		Status:        q.Get("status"),
		Priority:      q.Get("priority"),
		Category:      q.Get("category"),
		RoleName:      q.Get("role_scope"),
		Search:        q.Get("search"),
		AssignedToMe:  q.Get("assigned_to_me") == "true",
		Unassigned:    q.Get("unassigned") == "true",
		Overdue:       q.Get("overdue") == "true",
		IncludeClosed: q.Get("include_closed") == "true",
	}
	if ref := q.Get("assignee"); ref != "" {
		id, msg := a.svc.ResolveAssignee(r.Context(), caller, ref)
		if msg != "" {
			taskErr(w, http.StatusBadRequest, msg)
			return
		}
		f.AssigneeUserID = id
	}
	if cs := q.Get("closed_since"); cs != "" {
		t, msg := parseYMD("closed_since", cs)
		if msg != "" {
			taskErr(w, http.StatusBadRequest, msg)
			return
		}
		f.ClosedSince = t
	}
	tasks, err := a.svc.List(r.Context(), caller, f)
	if err != nil {
		a.taskServiceErr(w, r, err, caller, f.RoleName)
		return
	}
	views, err := enrichTasks(r.Context(), a.svc.pool, caller.TenantID, tasks)
	if err != nil {
		slog.Error("task: enriching list", "error", err)
		taskErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	taskJSON(w, http.StatusOK, map[string]any{"tasks": views})
}

type createBody struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Priority    string `json:"priority"`
	RoleScope   string `json:"role_scope"`
	Assignee    string `json:"assignee"`
	DueDate     string `json:"due_date"`
}

func (a *TaskApp) handleCreate(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	var body createBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		taskErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Title == "" {
		taskErr(w, http.StatusBadRequest, "title is required")
		return
	}
	in := CreateInput{
		Title:       body.Title,
		Description: body.Description,
		Priority:    body.Priority,
		RoleName:    body.RoleScope,
	}
	if body.Assignee != "" {
		id, msg := a.svc.ResolveAssignee(r.Context(), caller, body.Assignee)
		if msg != "" {
			taskErr(w, http.StatusBadRequest, msg)
			return
		}
		in.AssigneeUserID = id
	}
	if body.DueDate != "" {
		d, msg := parseYMD("due_date", body.DueDate)
		if msg != "" {
			taskErr(w, http.StatusBadRequest, msg)
			return
		}
		in.DueDate = d
	}
	t, err := a.svc.Create(r.Context(), caller, in)
	if err != nil {
		a.taskServiceErr(w, r, err, caller, in.RoleName)
		return
	}
	a.writeOne(w, r, caller, t)
}

type updateBody struct {
	Title         *string `json:"title"`
	Description   *string `json:"description"`
	Status        *string `json:"status"`
	Priority      *string `json:"priority"`
	Category      *string `json:"category"`
	BlockedReason *string `json:"blocked_reason"`
	Assignee      *string `json:"assignee"`
	ClearAssignee bool    `json:"clear_assignee"`
	RoleScope     *string `json:"role_scope"`
	DueDate       *string `json:"due_date"`
	ClearDueDate  bool    `json:"clear_due_date"`
}

func (a *TaskApp) handleUpdate(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := taskID(w, r)
	if !ok {
		return
	}
	var body updateBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		taskErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	u := UpdateInput{
		Title:         body.Title,
		Description:   body.Description,
		Status:        body.Status,
		Priority:      body.Priority,
		Category:      body.Category,
		BlockedReason: body.BlockedReason,
		ClearAssignee: body.ClearAssignee,
		ClearDueDate:  body.ClearDueDate,
		NewRoleName:   body.RoleScope,
	}
	if body.Assignee != nil && *body.Assignee != "" {
		aid, msg := a.svc.ResolveAssignee(r.Context(), caller, *body.Assignee)
		if msg != "" {
			taskErr(w, http.StatusBadRequest, msg)
			return
		}
		u.NewAssigneeUserID = aid
	}
	if body.DueDate != nil && *body.DueDate != "" {
		d, msg := parseYMD("due_date", *body.DueDate)
		if msg != "" {
			taskErr(w, http.StatusBadRequest, msg)
			return
		}
		u.DueDate = d
	}
	roleName := ""
	if body.RoleScope != nil {
		roleName = *body.RoleScope
	}
	t, err := a.svc.Update(r.Context(), caller, id, u)
	if err != nil {
		a.taskServiceErr(w, r, err, caller, roleName)
		return
	}
	a.writeOne(w, r, caller, t)
}

func (a *TaskApp) handleGet(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := taskID(w, r)
	if !ok {
		return
	}
	t, events, err := a.svc.Get(r.Context(), caller, id)
	if err != nil {
		a.taskServiceErr(w, r, err, caller, "")
		return
	}
	views, err := enrichTasks(r.Context(), a.svc.pool, caller.TenantID, []Task{*t})
	if err != nil {
		taskErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	evViews, err := enrichEvents(r.Context(), a.svc.pool, caller.TenantID, events)
	if err != nil {
		taskErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	taskJSON(w, http.StatusOK, map[string]any{"task": views[0], "events": evViews})
}

func (a *TaskApp) handleCategorize(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	n, err := a.svc.CategorizeUncategorized(r.Context(), caller)
	if err != nil {
		a.taskServiceErr(w, r, err, caller, "")
		return
	}
	taskJSON(w, http.StatusOK, map[string]any{"queued": n})
}

func (a *TaskApp) handleComplete(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := taskID(w, r)
	if !ok {
		return
	}
	t, err := a.svc.Complete(r.Context(), caller, id)
	if err != nil {
		a.taskServiceErr(w, r, err, caller, "")
		return
	}
	a.writeOne(w, r, caller, t)
}

func (a *TaskApp) handleSnooze(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := taskID(w, r)
	if !ok {
		return
	}
	var body struct {
		Days int `json:"days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		taskErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	t, err := a.svc.SnoozeDays(r.Context(), caller, id, body.Days)
	if err != nil {
		if msg, status, okk := mapTaskError(err, caller, ""); okk {
			taskErr(w, status, msg)
			return
		}
		taskErr(w, http.StatusBadRequest, err.Error())
		return
	}
	a.writeOne(w, r, caller, t)
}

func (a *TaskApp) handleComment(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := taskID(w, r)
	if !ok {
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Content == "" {
		taskErr(w, http.StatusBadRequest, "content is required")
		return
	}
	if err := a.svc.AddComment(r.Context(), caller, id, body.Content); err != nil {
		a.taskServiceErr(w, r, err, caller, "")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleMeta returns the picklists the UI needs: the caller's roles (for
// the role filter + create form) and the static status/priority sets.
func (a *TaskApp) handleMeta(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	categories, err := listCategories(r.Context(), a.svc.pool, caller.TenantID)
	if err != nil {
		slog.Error("task: listing categories for meta", "error", err)
	}
	if categories == nil {
		categories = []string{} // marshal as [] not null, honoring the API's string[]
	}
	taskJSON(w, http.StatusOK, map[string]any{
		"roles":      caller.Roles,
		"statuses":   []string{"open", "in_progress", "blocked", "done", "cancelled"},
		"priorities": Priorities,
		"categories": categories,
	})
}

// writeOne enriches and returns a single task as {"task": ...}.
func (a *TaskApp) writeOne(w http.ResponseWriter, r *http.Request, caller *services.Caller, t *Task) {
	views, err := enrichTasks(r.Context(), a.svc.pool, caller.TenantID, []Task{*t})
	if err != nil {
		taskErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	taskJSON(w, http.StatusOK, map[string]any{"task": views[0]})
}

// taskServiceErr maps a known service error to its status+message, else 500.
func (a *TaskApp) taskServiceErr(w http.ResponseWriter, _ *http.Request, err error, caller *services.Caller, roleName string) {
	if msg, status, ok := mapTaskError(err, caller, roleName); ok {
		taskErr(w, status, msg)
		return
	}
	slog.Error("task: unexpected service error", "error", err)
	taskErr(w, http.StatusInternalServerError, "internal error")
}

func taskID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		taskErr(w, http.StatusBadRequest, "invalid task id")
		return uuid.UUID{}, false
	}
	return id, true
}

func taskJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func taskErr(w http.ResponseWriter, status int, msg string) {
	taskJSON(w, status, map[string]any{"error": msg})
}
