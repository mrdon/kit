package services

import (
	"fmt"
	"strings"

	"github.com/mrdon/kit/internal/models"
)

// FormatSkillSummaries formats a list of skill summaries for display.
func FormatSkillSummaries(skills []models.SkillSummary) string {
	var b strings.Builder
	for _, s := range skills {
		id := s.ID.String()
		if id == "00000000-0000-0000-0000-000000000000" {
			id = "builtin"
		}
		fmt.Fprintf(&b, "- [%s] %s — %s (%s)\n", id, s.Name, s.Description, FormatScopes(s.Scopes))
	}
	return b.String()
}

// FormatScopes formats scope entries as a human-readable string.
func FormatScopes(scopes []models.SkillScope) string {
	if len(scopes) == 0 {
		return "no scopes"
	}
	parts := make([]string, len(scopes))
	for i, sc := range scopes {
		switch sc.ScopeType {
		case "platform":
			parts[i] = "built-in"
		case "tenant":
			parts[i] = "everyone"
		default:
			parts[i] = sc.ScopeType + ":" + sc.ScopeValue
		}
	}
	return strings.Join(parts, ", ")
}
