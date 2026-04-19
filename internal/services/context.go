package services

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/skills"
)

// BuildKnowledgeContext assembles the knowledge-relevant parts of the system context:
// business info, user roles, rules, skill catalog, and recent memories.
// This is shared between the Slack agent prompt and MCP resource.
// It does NOT include Slack-specific instructions or formatting rules.
func BuildKnowledgeContext(ctx context.Context, pool *pgxpool.Pool, c *Caller, tenant *models.Tenant) string {
	var parts []string

	// Business context
	if tenant.BusinessType != nil && *tenant.BusinessType != "" {
		parts = append(parts, "Business type: "+*tenant.BusinessType)
	}
	parts = append(parts, "Business timezone: "+tenant.Timezone)

	// User context
	parts = append(parts, fmt.Sprintf("User roles: %s (admin: %v)", strings.Join(c.Roles, ", "), c.IsAdmin))
	if len(c.Roles) == 0 {
		parts = append(parts, "User has no assigned roles.")
	}

	// Rules (scope-filtered)
	rules, _ := models.GetRulesForContext(ctx, pool, c.TenantID, c.Roles)
	if len(rules) > 0 {
		parts = append(parts, "\n## Rules")
		for _, r := range rules {
			parts = append(parts, "- "+r.Content)
		}
	}

	// Skill catalog (scope-filtered + built-ins)
	dbSkills, _ := models.GetSkillCatalog(ctx, pool, c.TenantID, c.UserID, c.RoleIDs)
	builtinSkills := skills.Builtins()
	if len(dbSkills) > 0 || len(builtinSkills) > 0 {
		parts = append(parts, buildSkillCatalog(dbSkills, builtinSkills))
	}

	// Recent memories (scope-filtered)
	memories, _ := models.GetRecentMemories(ctx, pool, c.TenantID, c.Identity, c.Roles, 5)
	if len(memories) > 0 {
		parts = append(parts, "\n## Remembered Facts")
		for _, m := range memories {
			parts = append(parts, "- "+m.Content)
		}
	}

	return strings.Join(parts, "\n\n")
}

func buildSkillCatalog(dbSkills []models.Skill, builtinSkills []skills.BuiltinSkill) string {
	var b strings.Builder
	b.WriteString("\n## Available Knowledge\n")
	b.WriteString("To read a skill's full content, use the `load_skill` tool with the skill's ID or name.\n")
	b.WriteString("To search across skills, use the `search_skills` tool.\n")
	for _, s := range builtinSkills {
		fmt.Fprintf(&b, "- [builtin] %s — %s\n", s.Name, s.Description)
	}
	for _, s := range dbSkills {
		fmt.Fprintf(&b, "- [%s] %s — %s\n", s.ID, s.Name, s.Description)
	}
	return b.String()
}
