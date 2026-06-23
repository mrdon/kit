package console

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/services"
)

// Skill file management (scripts, references attached to a skill). All
// admin-only; the SkillService independently enforces that too. Files belong
// to a DB skill — builtins have none.

// handleSkillFilesList lists the files attached to a skill.
func (a *App) handleSkillFilesList(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := skillID(w, r)
	if !ok {
		return
	}
	files, err := services.NewSkillService(a.pool).ListFiles(r.Context(), caller, id)
	if err != nil {
		writeSkillErr(w, err)
		return
	}
	out := make([]skillFileJSON, 0, len(files))
	for _, f := range files {
		out = append(out, skillFileJSON{ID: f.ID.String(), Filename: f.Filename})
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": out})
}

type skillFileBody struct {
	Filename string `json:"filename"`
	Content  string `json:"content"`
}

// handleSkillFileAdd attaches a file to a skill.
func (a *App) handleSkillFileAdd(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := skillID(w, r)
	if !ok {
		return
	}
	var body skillFileBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	if body.Filename == "" || body.Content == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "filename and content are required"})
		return
	}
	file, err := services.NewSkillService(a.pool).AddFile(r.Context(), caller, id, body.Filename, body.Content)
	if err != nil {
		writeSkillErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"file": skillFileJSON{ID: file.ID.String(), Filename: file.Filename},
	})
}

// handleSkillFileDelete removes a file by its own ID.
func (a *App) handleSkillFileDelete(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	fileID, err := uuid.Parse(r.PathValue("fileId"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid file id"})
		return
	}
	if err := services.NewSkillService(a.pool).DeleteFile(r.Context(), caller, fileID); err != nil {
		writeSkillErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
