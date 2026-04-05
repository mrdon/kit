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
		fmt.Fprintf(&b, "- [%s] %s — %s (%s)\n", s.ID, s.Name, s.Description, FormatScopes(s.Scopes))
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
		if sc.ScopeType == "tenant" {
			parts[i] = "everyone"
		} else {
			parts[i] = sc.ScopeType + ":" + sc.ScopeValue
		}
	}
	return strings.Join(parts, ", ")
}
