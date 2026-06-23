package console

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
)

// The skills pages are the console surface for the tenant knowledge base.
// Listing/loading is open to every caller (scope-filtered by the service);
// create/update/delete and file management are admin-only — enforced both by
// the AdminJSON middleware on the route AND independently by SkillService.
// These handlers are thin: all authorization and scope logic lives in
// services.SkillService, shared with the MCP/agent tools.

type skillScopeJSON struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type skillSummaryJSON struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Scopes      []skillScopeJSON `json:"scopes"`
	Builtin     bool             `json:"builtin"`
	Editable    bool             `json:"editable"`
}

type skillDetailJSON struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Content     string          `json:"content"`
	Builtin     bool            `json:"builtin"`
	Editable    bool            `json:"editable"`
	Files       []skillFileJSON `json:"files"`
}

type skillFileJSON struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
}

// handleSkillsList returns the caller's visible skills (admins see all).
func (a *App) handleSkillsList(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	skills, err := services.NewSkillService(a.pool).List(r.Context(), caller, r.URL.Query().Get("search"))
	if err != nil {
		writeSkillErr(w, err)
		return
	}
	out := make([]skillSummaryJSON, 0, len(skills))
	for _, s := range skills {
		builtin := s.ID == uuid.Nil
		scopes := make([]skillScopeJSON, 0, len(s.Scopes))
		for _, sc := range s.Scopes {
			scopes = append(scopes, skillScopeJSON{Type: string(sc.ScopeType), Value: sc.ScopeValue})
		}
		id := ""
		if !builtin {
			id = s.ID.String()
		}
		out = append(out, skillSummaryJSON{
			ID: id, Name: s.Name, Description: s.Description,
			Scopes: scopes, Builtin: builtin, Editable: !builtin,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": out})
}

// handleSkillsMeta returns the picklists the skill forms need: the caller's
// roles (for the create-scope select) and is_admin (for client-side button
// gating). Pure caller data — no business logic.
func (a *App) handleSkillsMeta(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	roles := caller.Roles
	if roles == nil {
		roles = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"roles":    roles,
		"is_admin": caller.IsAdmin,
		// The catchall role every user holds. The UI presents a skill
		// scoped to it as "All members" (distinct from tenant:* which is
		// also public to the website widget).
		"catchall_role": models.RoleMember,
	})
}

// handleSkillGet returns one skill with its files. The {id} segment may be a
// UUID (a tenant DB skill) or a builtin slug name — builtins resolve via the
// service and come back read-only (zero UUID, no files).
func (a *App) handleSkillGet(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	svc := services.NewSkillService(a.pool)
	raw := r.PathValue("id")

	var (
		skill *models.Skill
		files []models.SkillFile
		err   error
	)
	if id, perr := uuid.Parse(raw); perr == nil {
		skill, files, err = svc.Load(r.Context(), caller, id)
	} else {
		skill, files, err = svc.ResolveByName(r.Context(), caller, raw)
	}
	if err != nil {
		writeSkillErr(w, err)
		return
	}

	builtin := skill.ID == uuid.Nil
	detail := skillDetailJSON{
		Name: skill.Name, Description: skill.Description, Content: skill.Content,
		Builtin: builtin, Editable: !builtin, Files: make([]skillFileJSON, 0, len(files)),
	}
	if !builtin {
		detail.ID = skill.ID.String()
	}
	for _, f := range files {
		detail.Files = append(detail.Files, skillFileJSON{ID: f.ID.String(), Filename: f.Filename})
	}
	writeJSON(w, http.StatusOK, map[string]any{"skill": detail})
}

type skillCreateBody struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content"`
	Scope       string `json:"scope"`
}

// handleSkillCreate creates a skill. Admin-only (AdminJSON + service guard).
func (a *App) handleSkillCreate(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	var body skillCreateBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	if body.Name == "" || body.Description == "" || body.Content == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name, description and content are required"})
		return
	}
	skill, err := services.NewSkillService(a.pool).Create(
		r.Context(), caller, body.Name, body.Description, body.Content, "", body.Scope)
	if err != nil {
		writeSkillErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"skill": map[string]any{"id": skill.ID.String()}})
}

type skillUpdateBody struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Content     *string `json:"content"`
}

// handleSkillUpdate updates a skill's name/description/content. Admin-only.
func (a *App) handleSkillUpdate(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := skillID(w, r)
	if !ok {
		return
	}
	var body skillUpdateBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	if err := services.NewSkillService(a.pool).Update(
		r.Context(), caller, id, body.Name, body.Description, body.Content); err != nil {
		writeSkillErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSkillDelete deletes a skill. Admin-only.
func (a *App) handleSkillDelete(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := skillID(w, r)
	if !ok {
		return
	}
	if err := services.NewSkillService(a.pool).Delete(r.Context(), caller, id); err != nil {
		writeSkillErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// skillID parses the {id} path segment as a UUID.
func skillID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid skill id"})
		return uuid.UUID{}, false
	}
	return id, true
}

// writeSkillErr maps a SkillService error to a status + JSON message.
func writeSkillErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, services.ErrForbidden):
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "Permission denied."})
	case errors.Is(err, services.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "Skill not found."})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
	}
}
