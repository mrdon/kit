package services

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/skills"
)

// SkillTools defines the shared tool metadata for skill operations.
var SkillTools = []ToolMeta{
	{Name: "search_skills", Description: "Search knowledge base for relevant skills using full-text search.", Schema: propsReq(map[string]any{"query": field("string", "Search query")}, "query")},
	{Name: "load_skill", Description: "Load the full content of a specific skill by ID or name.", Schema: propsReq(map[string]any{"skill_id": field("string", "The skill UUID or built-in skill name")}, "skill_id")},
	{Name: "load_skill_file", Description: "Load a file attached to a skill by its ID.", Schema: propsReq(map[string]any{"file_id": field("string", "The file UUID")}, "file_id")},
	{Name: "list_skills", Description: "List skills you have access to, with scope info. Admins see all skills.", Schema: props(map[string]any{"search": field("string", "Optional search filter on name or description")})},
	{Name: "create_skill", Description: "Create a new skill (knowledge article). Updates if name already exists.", Schema: propsReq(map[string]any{
		"name": field("string", "Skill name"), "description": field("string", "Brief description"),
		"content": field("string", "Full content (markdown)"), "scope": field("string", "Scope: 'tenant' for everyone, or a role name"),
	}, "name", "description", "content"), AdminOnly: true},
	{Name: "update_skill", Description: "Update an existing skill's content.", Schema: propsReq(map[string]any{
		"skill_id": field("string", "The skill UUID"), "name": field("string", "New name (optional)"),
		"description": field("string", "New description (optional)"), "content": field("string", "New content (optional)"),
	}, "skill_id"), AdminOnly: true},
	{Name: "delete_skill", Description: "Delete a skill.", Schema: propsReq(map[string]any{"skill_id": field("string", "The skill UUID")}, "skill_id"), AdminOnly: true},
	{Name: "add_skill_file", Description: "Attach a file to a skill (script, reference, image, etc.).", Schema: propsReq(map[string]any{
		"skill_id": field("string", "The skill UUID"), "filename": field("string", "Filename (e.g., 'setup.sh')"),
		"content": field("string", "File content"),
	}, "skill_id", "filename", "content"), AdminOnly: true},
	{Name: "list_skill_files", Description: "List files attached to a skill.", Schema: propsReq(map[string]any{"skill_id": field("string", "The skill UUID")}, "skill_id"), AdminOnly: true},
	{Name: "delete_skill_file", Description: "Delete a file attached to a skill.", Schema: propsReq(map[string]any{"file_id": field("string", "The file UUID")}, "file_id"), AdminOnly: true},
}

// SkillService handles skill operations with authorization.
type SkillService struct {
	pool *pgxpool.Pool
}

// Search searches skills visible to the caller.
func (s *SkillService) Search(ctx context.Context, c *Caller, query string) ([]models.Skill, error) {
	return models.SearchSkills(ctx, s.pool, c.TenantID, c.UserID, c.RoleIDs, query)
}

// Load returns a skill by ID with authorization check.
// Also accepts built-in skill names (e.g. "user-guide").
func (s *SkillService) Load(ctx context.Context, c *Caller, skillID uuid.UUID) (*models.Skill, []models.SkillFile, error) {
	skill, err := models.GetSkill(ctx, s.pool, c.TenantID, skillID)
	if err != nil {
		return nil, nil, fmt.Errorf("loading skill: %w", err)
	}
	if skill == nil {
		return nil, nil, ErrNotFound
	}
	if !c.IsAdmin {
		if err := s.checkSkillAccess(ctx, c, skillID); err != nil {
			return nil, nil, err
		}
	}
	files, _ := models.ListSkillFiles(ctx, s.pool, c.TenantID, skillID)
	return skill, files, nil
}

// LoadByName returns a built-in skill by name. Admin-only builtins are
// hidden from non-admin callers (returned as ErrNotFound so the LLM doesn't
// leak the existence of an admin-only skill via a "forbidden" error path).
// c may be nil for legacy call sites that pre-date role gating; in that
// case admin-only skills remain loadable (no caller to check against).
func (s *SkillService) LoadByName(c *Caller, name string) (*models.Skill, error) {
	b := skills.GetBuiltin(name)
	if b == nil {
		return nil, ErrNotFound
	}
	if b.AdminOnly && c != nil && !c.IsAdmin {
		return nil, ErrNotFound
	}
	return &models.Skill{
		Name:        b.Name,
		Description: b.Description,
		Content:     b.Content,
	}, nil
}

// ResolveByName looks up a skill by its slug name, trying tenant DB
// skills first and then builtins. Returns the skill plus its files when
// the DB path matches (builtins have no attached files). Used by
// load_skill so task-scheduled prompts like `load_skill skill_id=foo`
// resolve the tenant's own skills without needing a UUID.
func (s *SkillService) ResolveByName(ctx context.Context, c *Caller, name string) (*models.Skill, []models.SkillFile, error) {
	skill, err := models.GetSkillByName(ctx, s.pool, c.TenantID, name)
	if err != nil {
		return nil, nil, fmt.Errorf("loading skill by name: %w", err)
	}
	if skill != nil {
		if !c.IsAdmin {
			if err := s.checkSkillAccess(ctx, c, skill.ID); err != nil {
				return nil, nil, err
			}
		}
		files, _ := models.ListSkillFiles(ctx, s.pool, c.TenantID, skill.ID)
		return skill, files, nil
	}
	b, berr := s.LoadByName(c, name)
	if berr != nil {
		return nil, nil, berr
	}
	return b, nil, nil
}

// LoadFile returns a skill file by ID with authorization on the parent skill.
func (s *SkillService) LoadFile(ctx context.Context, c *Caller, fileID uuid.UUID) (*models.SkillFile, error) {
	ref, err := models.GetSkillReference(ctx, s.pool, c.TenantID, fileID)
	if err != nil {
		return nil, fmt.Errorf("loading skill file: %w", err)
	}
	if ref == nil {
		return nil, ErrNotFound
	}
	if !c.IsAdmin {
		if err := s.checkSkillAccess(ctx, c, ref.SkillID); err != nil {
			return nil, err
		}
	}
	return ref, nil
}

// List returns skills visible to the caller with optional search.
// Admins see all skills; non-admins see only scope-matched skills.
// Built-in skills are included at the top of the list.
func (s *SkillService) List(ctx context.Context, c *Caller, search string) ([]models.SkillSummary, error) {
	dbSkills, err := models.ListSkillsFiltered(ctx, s.pool, c.TenantID, c.IsAdmin, c.UserID, c.RoleIDs, search)
	if err != nil {
		return nil, err
	}
	builtins := skills.VisibleMatchBuiltins(search, c.IsAdmin)
	result := make([]models.SkillSummary, 0, len(builtins)+len(dbSkills))
	for _, b := range builtins {
		result = append(result, models.SkillSummary{
			Name:        b.Name,
			Description: b.Description,
			Scopes:      []models.SkillScope{{ScopeType: models.ScopeTypePlatform, ScopeValue: models.ScopeValueAll}},
		})
	}
	return append(result, dbSkills...), nil
}

// Create creates a new skill. Admin only.
func (s *SkillService) Create(ctx context.Context, c *Caller, name, description, content, source, scope string) (*models.Skill, error) {
	if !c.IsAdmin {
		return nil, ErrForbidden
	}
	if scope == "" {
		scope = string(models.ScopeTypeTenant)
	}
	content = stripSkillFrontmatter(content)
	return models.CreateSkill(ctx, s.pool, c.TenantID, name, description, content, source, scope)
}

// Update updates a skill. Admin only.
func (s *SkillService) Update(ctx context.Context, c *Caller, skillID uuid.UUID, name, description, content *string) error {
	if !c.IsAdmin {
		return ErrForbidden
	}
	if content != nil {
		stripped := stripSkillFrontmatter(*content)
		content = &stripped
	}
	return models.UpdateSkill(ctx, s.pool, c.TenantID, skillID, name, description, content)
}

// stripSkillFrontmatter removes a leading SKILL.md `---`-delimited block
// from content so it isn't stored alongside the explicit name/description
// columns. ToSKILLMD prepends a fresh frontmatter block on read; without
// this, callers that pass a full SKILL.md as `content` end up with the
// frontmatter rendered twice in load_skill output.
func stripSkillFrontmatter(content string) string {
	_, _, body, err := models.ParseSKILLMD(content)
	if err != nil || body == "" {
		return content
	}
	return body
}

// Delete deletes a skill. Admin only.
func (s *SkillService) Delete(ctx context.Context, c *Caller, skillID uuid.UUID) error {
	if !c.IsAdmin {
		return ErrForbidden
	}
	return models.DeleteSkill(ctx, s.pool, c.TenantID, skillID)
}

// AddFile attaches a file to a skill. Admin only.
func (s *SkillService) AddFile(ctx context.Context, c *Caller, skillID uuid.UUID, filename, content string) (*models.SkillFile, error) {
	if !c.IsAdmin {
		return nil, ErrForbidden
	}
	return models.AddSkillFile(ctx, s.pool, c.TenantID, skillID, filename, content)
}

// ListFiles lists files attached to a skill. Admin only.
func (s *SkillService) ListFiles(ctx context.Context, c *Caller, skillID uuid.UUID) ([]models.SkillFile, error) {
	if !c.IsAdmin {
		return nil, ErrForbidden
	}
	return models.ListSkillFiles(ctx, s.pool, c.TenantID, skillID)
}

// DeleteFile deletes a file attached to a skill. Admin only.
func (s *SkillService) DeleteFile(ctx context.Context, c *Caller, fileID uuid.UUID) error {
	if !c.IsAdmin {
		return ErrForbidden
	}
	return models.DeleteSkillFile(ctx, s.pool, c.TenantID, fileID)
}

// checkSkillAccess verifies the caller can see a skill via Caller.CanSee.
func (s *SkillService) checkSkillAccess(ctx context.Context, c *Caller, skillID uuid.UUID) error {
	scopes, err := models.GetSkillScopeRefs(ctx, s.pool, c.TenantID, skillID)
	if err != nil {
		return fmt.Errorf("checking skill access: %w", err)
	}
	if !c.CanSee(scopes) {
		return ErrForbidden
	}
	return nil
}
