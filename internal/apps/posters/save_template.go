package posters

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

// SaveAsTemplate turns a poster's current source back into a template:
// inlined copy becomes content fields, chosen photos become photo slots,
// one-off tweaks stay as layout. One Sonnet call, with one retry carrying
// the template check's problems, then a draft template.
func (a *App) SaveAsTemplate(ctx context.Context, tenantID uuid.UUID, posterID uuid.UUID, by *uuid.UUID) (*Template, error) {
	poster, cur, err := a.loadPoster(ctx, tenantID, posterID)
	if err != nil {
		return nil, err
	}
	if cur == nil {
		return nil, invalid("this poster has no version yet")
	}
	var out struct {
		Source      string `json:"source"`
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	var problems []string
	var chk *TemplateCheckOutcome
	for attempt := range 2 {
		user := mustRender("user_save_template.tmpl", map[string]any{"Source": cur.Source, "Problems": problems})
		if err := askJSON(ctx, a.llm, mustRender("system_save_template.tmpl", nil), user, nil, &out); err != nil {
			return nil, err
		}
		if strings.TrimSpace(out.Source) == "" {
			return nil, invalid("the model returned no template source")
		}
		if chk, err = a.templateCheck(ctx, tenantID, out.Source); err != nil {
			return nil, err
		}
		if len(chk.Problems) == 0 {
			break
		}
		problems = chk.Problems
		if attempt == 1 {
			return nil, invalid("the template check failed twice: %s", strings.Join(problems, "; "))
		}
	}
	name := strOr(out.Name, chk.Meta.Name)
	if name == "" {
		name = "From " + strOr(poster.Title, "poster")
	}
	return createTemplate(ctx, a.pool, tenantID, TemplateInput{
		Name: name, Description: strOr(out.Description, chk.Meta.Description), Origin: OriginPoster, Meta: *chk.Meta,
		Source: out.Source, Summary: "Saved from poster " + strOr(poster.Title, posterID.String()), Author: AuthorAgent,
		SourcePosterID: &posterID, CreatedBy: by,
	})
}
