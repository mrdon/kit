package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/services"
)

func registerSkillTools(r *Registry, isAdmin bool) {
	for _, meta := range services.SkillTools {
		if meta.AdminOnly && !isAdmin {
			continue
		}
		r.Register(Def{
			Name:        meta.Name,
			Description: meta.Description,
			Schema:      meta.Schema,
			AdminOnly:   meta.AdminOnly,
			Handler:     skillHandler(meta.Name),
		})
	}
}

func skillHandler(name string) HandlerFunc {
	switch name {
	case "search_skills":
		return handleSearchSkills
	case "load_skill":
		return handleLoadSkill
	case "load_skill_file":
		return handleLoadSkillFile
	case "list_skills":
		return handleListSkills
	case "create_skill":
		return handleCreateSkill
	case "update_skill":
		return handleUpdateSkill
	case "delete_skill":
		return handleDeleteSkill
	case "add_skill_file":
		return handleAddSkillFile
	case "list_skill_files":
		return handleListSkillFiles
	case "delete_skill_file":
		return handleDeleteSkillFile
	default:
		return func(_ *ExecContext, _ json.RawMessage) (string, error) {
			return "", fmt.Errorf("unknown skill tool: %s", name)
		}
	}
}

func handleSearchSkills(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	results, err := ec.Svc.Skills.Search(ec.Ctx, ec.Caller(), inp.Query)
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return "No matching skills found.", nil
	}
	var b strings.Builder
	b.WriteString("Search results:\n")
	for _, s := range results {
		fmt.Fprintf(&b, "- [%s] %s — %s\n", s.ID, s.Name, s.Description)
	}
	return b.String(), nil
}

func handleLoadSkill(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		SkillID string `json:"skill_id"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	skillID, err := uuid.Parse(inp.SkillID)
	if err != nil {
		// Not a UUID — try as a slug name. ResolveByName checks tenant
		// skills first, then builtins, so task-scheduled prompts like
		// `load_skill skill_id=daily-standup` resolve without a UUID.
		skill, files, rerr := ec.Svc.Skills.ResolveByName(ec.Ctx, ec.Caller(), inp.SkillID)
		if errors.Is(rerr, services.ErrNotFound) {
			return "Skill not found.", nil
		}
		if errors.Is(rerr, services.ErrForbidden) {
			return "You don't have access to this skill.", nil
		}
		if rerr != nil {
			return "", rerr
		}
		var b strings.Builder
		b.WriteString(skill.ToSKILLMD())
		if len(files) > 0 {
			b.WriteString("\n\n## Files\n")
			for _, f := range files {
				fmt.Fprintf(&b, "- [%s] %s\n", f.ID, f.Filename)
			}
			b.WriteString("\nUse load_skill_file to read a specific file.")
		}
		return b.String(), nil
	}
	skill, files, err := ec.Svc.Skills.Load(ec.Ctx, ec.Caller(), skillID)
	if errors.Is(err, services.ErrNotFound) {
		return "Skill not found.", nil
	}
	if errors.Is(err, services.ErrForbidden) {
		return "You don't have access to this skill.", nil
	}
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(skill.ToSKILLMD())
	if len(files) > 0 {
		b.WriteString("\n\n## Files\n")
		for _, f := range files {
			fmt.Fprintf(&b, "- [%s] %s\n", f.ID, f.Filename)
		}
		b.WriteString("\nUse load_skill_file to read a specific file.")
	}
	return b.String(), nil
}

func handleLoadSkillFile(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		FileID string `json:"file_id"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	fileID, err := uuid.Parse(inp.FileID)
	if err != nil {
		return "Invalid file ID.", nil
	}
	ref, err := ec.Svc.Skills.LoadFile(ec.Ctx, ec.Caller(), fileID)
	if errors.Is(err, services.ErrNotFound) {
		return "File not found.", nil
	}
	if errors.Is(err, services.ErrForbidden) {
		return "You don't have access to this file.", nil
	}
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("# %s\n\n%s", ref.Filename, ref.Content), nil
}

func handleListSkills(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		Search string `json:"search"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	skills, err := ec.Svc.Skills.List(ec.Ctx, ec.Caller(), inp.Search)
	if err != nil {
		return "", err
	}
	if len(skills) == 0 {
		return "No skills found.", nil
	}
	return "Skills:\n" + services.FormatSkillSummaries(skills), nil
}

func handleCreateSkill(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		Name, Description, Content, Scope string
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	skill, err := ec.Svc.Skills.Create(ec.Ctx, ec.Caller(), inp.Name, inp.Description, inp.Content, "chat", inp.Scope)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Skill '%s' created (ID: %s).", skill.Name, skill.ID), nil
}

func handleUpdateSkill(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		SkillID     string  `json:"skill_id"`
		Name        *string `json:"name"`
		Description *string `json:"description"`
		Content     *string `json:"content"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	skillID, err := uuid.Parse(inp.SkillID)
	if err != nil {
		return "Invalid skill ID.", nil
	}
	if err := ec.Svc.Skills.Update(ec.Ctx, ec.Caller(), skillID, inp.Name, inp.Description, inp.Content); err != nil {
		return "", err
	}
	return "Skill updated.", nil
}

func handleDeleteSkill(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		SkillID string `json:"skill_id"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	skillID, err := uuid.Parse(inp.SkillID)
	if err != nil {
		return "Invalid skill ID.", nil
	}
	if err := ec.Svc.Skills.Delete(ec.Ctx, ec.Caller(), skillID); err != nil {
		return "", err
	}
	return "Skill deleted.", nil
}

func handleAddSkillFile(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct{ SkillID, Filename, Content string }
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	skillID, err := uuid.Parse(inp.SkillID)
	if err != nil {
		return "Invalid skill ID.", nil
	}
	f, err := ec.Svc.Skills.AddFile(ec.Ctx, ec.Caller(), skillID, inp.Filename, inp.Content)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("File '%s' attached (ID: %s).", f.Filename, f.ID), nil
}

func handleListSkillFiles(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		SkillID string `json:"skill_id"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	skillID, err := uuid.Parse(inp.SkillID)
	if err != nil {
		return "Invalid skill ID.", nil
	}
	files, err := ec.Svc.Skills.ListFiles(ec.Ctx, ec.Caller(), skillID)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "No files attached.", nil
	}
	var b strings.Builder
	b.WriteString("Files:\n")
	for _, f := range files {
		fmt.Fprintf(&b, "- [%s] %s\n", f.ID, f.Filename)
	}
	return b.String(), nil
}

func handleDeleteSkillFile(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		FileID string `json:"file_id"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	fileID, err := uuid.Parse(inp.FileID)
	if err != nil {
		return "Invalid file ID.", nil
	}
	if err := ec.Svc.Skills.DeleteFile(ec.Ctx, ec.Caller(), fileID); err != nil {
		return "", err
	}
	return "File deleted.", nil
}
