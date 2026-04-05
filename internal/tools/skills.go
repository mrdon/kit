package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
)

func registerSkillTools(r *Registry, isAdmin bool) {
	registerSkillUserTools(r)
	if isAdmin {
		registerSkillAdminTools(r)
	}
}

func registerSkillUserTools(r *Registry) {
	r.Register(Def{
		Name:        "search_skills",
		Description: "Search knowledge base for relevant skills using full-text search.",
		Schema:      propsReq(map[string]any{"query": field("string", "Search query")}, "query"),
		Handler: func(ec *ExecContext, input json.RawMessage) (string, error) {
			var inp struct {
				Query string `json:"query"`
			}
			if err := json.Unmarshal(input, &inp); err != nil {
				return "", err
			}
			userRoles, _ := models.GetUserRoleNames(ec.Ctx, ec.Pool, ec.Tenant.ID, ec.User.ID, ec.Tenant.DefaultRoleID)
			results, err := models.SearchSkills(ec.Ctx, ec.Pool, ec.Tenant.ID, userRoles, inp.Query)
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
		},
	})

	r.Register(Def{
		Name:        "load_skill",
		Description: "Load the full content of a specific skill by ID.",
		Schema:      propsReq(map[string]any{"skill_id": field("string", "The skill UUID")}, "skill_id"),
		Handler: func(ec *ExecContext, input json.RawMessage) (string, error) {
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
			skill, err := models.GetSkill(ec.Ctx, ec.Pool, ec.Tenant.ID, skillID)
			if err != nil {
				return "", err
			}
			if skill == nil {
				return "Skill not found.", nil
			}
			var b strings.Builder
			b.WriteString(skill.ToSKILLMD())
			files, _ := models.ListSkillFiles(ec.Ctx, ec.Pool, ec.Tenant.ID, skillID)
			if len(files) > 0 {
				b.WriteString("\n\n## Files\n")
				for _, f := range files {
					fmt.Fprintf(&b, "- [%s] %s\n", f.ID, f.Filename)
				}
				b.WriteString("\nUse load_skill_file to read a specific file.")
			}
			return b.String(), nil
		},
	})

	r.Register(Def{
		Name:        "load_skill_file",
		Description: "Load a file attached to a skill by its ID.",
		Schema:      propsReq(map[string]any{"file_id": field("string", "The file UUID")}, "file_id"),
		Handler: func(ec *ExecContext, input json.RawMessage) (string, error) {
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
			ref, err := models.GetSkillReference(ec.Ctx, ec.Pool, ec.Tenant.ID, fileID)
			if err != nil {
				return "", err
			}
			if ref == nil {
				return "File not found.", nil
			}
			return fmt.Sprintf("# %s\n\n%s", ref.Filename, ref.Content), nil
		},
	})
}

func registerSkillAdminTools(r *Registry) {
	r.Register(Def{
		Name:        "list_skills",
		Description: "List all skills (all scopes).",
		Schema:      props(map[string]any{}),
		AdminOnly:   true,
		Handler: func(ec *ExecContext, _ json.RawMessage) (string, error) {
			skills, err := models.ListSkills(ec.Ctx, ec.Pool, ec.Tenant.ID)
			if err != nil {
				return "", err
			}
			if len(skills) == 0 {
				return "No skills defined yet.", nil
			}
			var b strings.Builder
			b.WriteString("Skills:\n")
			for _, s := range skills {
				fmt.Fprintf(&b, "- [%s] %s — %s\n", s.ID, s.Name, s.Description)
			}
			return b.String(), nil
		},
	})

	r.Register(Def{
		Name:        "create_skill",
		Description: "Create a new skill (knowledge article). Updates if name already exists.",
		Schema: propsReq(map[string]any{
			"name":        field("string", "Skill name"),
			"description": field("string", "Brief description"),
			"content":     field("string", "Full content (markdown)"),
			"scope":       field("string", "Scope: 'tenant' for everyone, or a role name"),
		}, "name", "description", "content"),
		AdminOnly: true,
		Handler: func(ec *ExecContext, input json.RawMessage) (string, error) {
			var inp struct {
				Name, Description, Content, Scope string
			}
			if err := json.Unmarshal(input, &inp); err != nil {
				return "", err
			}
			scope := inp.Scope
			if scope == "" {
				scope = "tenant"
			}
			skill, err := models.CreateSkill(ec.Ctx, ec.Pool, ec.Tenant.ID, inp.Name, inp.Description, inp.Content, "chat", scope)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Skill '%s' created (ID: %s).", skill.Name, skill.ID), nil
		},
	})

	r.Register(Def{
		Name:        "update_skill",
		Description: "Update an existing skill's content.",
		Schema: propsReq(map[string]any{
			"skill_id":    field("string", "The skill UUID"),
			"name":        field("string", "New name (optional)"),
			"description": field("string", "New description (optional)"),
			"content":     field("string", "New content (optional)"),
		}, "skill_id"),
		AdminOnly: true,
		Handler: func(ec *ExecContext, input json.RawMessage) (string, error) {
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
			if err := models.UpdateSkill(ec.Ctx, ec.Pool, ec.Tenant.ID, skillID, inp.Name, inp.Description, inp.Content); err != nil {
				return "", err
			}
			return "Skill updated.", nil
		},
	})

	r.Register(Def{
		Name:        "delete_skill",
		Description: "Delete a skill.",
		Schema:      propsReq(map[string]any{"skill_id": field("string", "The skill UUID")}, "skill_id"),
		AdminOnly:   true,
		Handler: func(ec *ExecContext, input json.RawMessage) (string, error) {
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
			if err := models.DeleteSkill(ec.Ctx, ec.Pool, ec.Tenant.ID, skillID); err != nil {
				return "", err
			}
			return "Skill deleted.", nil
		},
	})

	r.Register(Def{
		Name:        "add_skill_file",
		Description: "Attach a file to a skill (script, reference, image, etc.).",
		Schema: propsReq(map[string]any{
			"skill_id": field("string", "The skill UUID"),
			"filename": field("string", "Filename (e.g., 'setup.sh')"),
			"content":  field("string", "File content"),
		}, "skill_id", "filename", "content"),
		AdminOnly: true,
		Handler: func(ec *ExecContext, input json.RawMessage) (string, error) {
			var inp struct {
				SkillID, Filename, Content string
			}
			if err := json.Unmarshal(input, &inp); err != nil {
				return "", err
			}
			skillID, err := uuid.Parse(inp.SkillID)
			if err != nil {
				return "Invalid skill ID.", nil
			}
			f, err := models.AddSkillFile(ec.Ctx, ec.Pool, ec.Tenant.ID, skillID, inp.Filename, inp.Content)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("File '%s' attached (ID: %s).", f.Filename, f.ID), nil
		},
	})

	r.Register(Def{
		Name:        "list_skill_files",
		Description: "List files attached to a skill.",
		Schema:      propsReq(map[string]any{"skill_id": field("string", "The skill UUID")}, "skill_id"),
		AdminOnly:   true,
		Handler: func(ec *ExecContext, input json.RawMessage) (string, error) {
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
			files, err := models.ListSkillFiles(ec.Ctx, ec.Pool, ec.Tenant.ID, skillID)
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
		},
	})

	r.Register(Def{
		Name:        "delete_skill_file",
		Description: "Delete a file attached to a skill.",
		Schema:      propsReq(map[string]any{"file_id": field("string", "The file UUID")}, "file_id"),
		AdminOnly:   true,
		Handler: func(ec *ExecContext, input json.RawMessage) (string, error) {
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
			if err := models.DeleteSkillFile(ec.Ctx, ec.Pool, ec.Tenant.ID, fileID); err != nil {
				return "", err
			}
			return "File deleted.", nil
		},
	})
}
