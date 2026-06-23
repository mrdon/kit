package console

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
)

// The jobs pages are the console surface for scheduled jobs. Listing and
// per-job edit/delete are open to any caller; visibility and the right to
// manage a given job are enforced entirely by services.JobService —
// non-admins are limited to their visible scope, admins manage tenant-wide.
// No create-from-scratch here (the agent owns scheduling); the console edits
// existing jobs' description, linked skill, and policy.

// handleJobsList returns the caller's manageable jobs, enriched for display.
func (a *App) handleJobsList(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	views, err := services.NewJobService(a.pool).ListViews(r.Context(), caller)
	if err != nil {
		writeJobErr(w, err)
		return
	}
	if views == nil {
		views = []services.JobView{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": views})
}

// handleJobGet returns one job the caller may manage.
func (a *App) handleJobGet(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := jobID(w, r)
	if !ok {
		return
	}
	view, err := services.NewJobService(a.pool).Get(r.Context(), caller, id)
	if err != nil {
		writeJobErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": view})
}

type jobUpdateBody struct {
	Description *string         `json:"description"`
	SkillName   *string         `json:"skill_name"`
	Policy      json.RawMessage `json:"policy"`
}

// handleJobUpdate edits a job's description, linked skill, and/or policy.
// Pointer/raw semantics match the MCP update_job tool: an omitted field is
// left untouched; skill_name="" clears the link; a provided policy object
// replaces the policy wholesale.
func (a *App) handleJobUpdate(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := jobID(w, r)
	if !ok {
		return
	}
	var body jobUpdateBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	in := services.UpdateInput{Description: body.Description, SkillName: body.SkillName}
	if len(body.Policy) > 0 && string(body.Policy) != "null" {
		policy, msg := parseJobPolicy(body.Policy)
		if msg != "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": msg})
			return
		}
		in.Policy = policy
	}
	if err := services.NewJobService(a.pool).Update(r.Context(), caller, id, in); err != nil {
		writeJobErr(w, err)
		return
	}
	view, err := services.NewJobService(a.pool).Get(r.Context(), caller, id)
	if err != nil {
		writeJobErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": view})
}

// handleJobDelete deletes a job the caller may manage.
func (a *App) handleJobDelete(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := jobID(w, r)
	if !ok {
		return
	}
	if err := services.NewJobService(a.pool).Delete(r.Context(), caller, id); err != nil {
		writeJobErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// parseJobPolicy decodes a policy object from the request, rejecting unknown
// fields. Policy only ever *constrains* the scheduled agent (allow-list,
// force-gate, pinned args), so it can't escalate privilege — no per-tool
// availability check is needed here (unlike the agent-side create path).
func parseJobPolicy(raw json.RawMessage) (*models.Policy, string) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var p models.Policy
	if err := dec.Decode(&p); err != nil {
		return nil, "Invalid policy: " + err.Error()
	}
	return &p, ""
}

// jobID parses the {id} path segment as a UUID.
func jobID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid job id"})
		return uuid.UUID{}, false
	}
	return id, true
}

// writeJobErr maps a JobService error to a status + JSON message.
func writeJobErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, services.ErrForbidden):
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "Permission denied."})
	case errors.Is(err, services.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "Job not found."})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
	}
}
