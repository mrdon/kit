package posters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/events"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/posterrender"
)

func jsonMarshal(v any) ([]byte, error)   { return json.Marshal(v) }
func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
func jsonIndent(v any) string             { b, _ := json.MarshalIndent(v, "", "  "); return string(b) }
func strOr(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}
func uuidPtr(id uuid.UUID) *uuid.UUID { return &id }
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// writeCopy turns an event's public fields into poster content: one Sonnet
// call, validated by the renderer's copy rules, with one retry carrying
// the errors. Problems that survive the retry come back as the error.
func (a *App) writeCopy(ctx context.Context, tenantID uuid.UUID, e *events.Event, brand *posterrender.Brand) (*posterrender.Content, error) {
	tenant, err := a.tenantName(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	system := mustRender("system_copy.tmpl", map[string]any{
		"Tenant": tenant,
		"Voice":  a.skillText(ctx, tenantID, copySkill),
		"Brand":  a.skillText(ctx, tenantID, brandingSkill),
	})
	facts := factsFor(e)
	eventText := "today: " + today(e.Loc()) + "\n" + factsText(facts)
	var problems []string
	for attempt := range 2 {
		user := mustRender("user_copy.tmpl", map[string]any{"Event": eventText, "Problems": problems})
		var c posterrender.Content
		if err := askJSON(ctx, a.llm, system, user, nil, &c); err != nil {
			return nil, fmt.Errorf("writing copy: %w", err)
		}
		normalizeContent(&c)
		check, err := a.renderer.CopyCheck(ctx, c, brand)
		if err != nil {
			return nil, fmt.Errorf("checking copy: %w", err)
		}
		if len(check.Problems) == 0 {
			return &c, nil
		}
		problems = check.Problems
		if attempt == 1 {
			return nil, fmt.Errorf("the copy failed the checks twice: %s", strings.Join(problems, "; "))
		}
	}
	return nil, errors.New("writing copy: no attempt succeeded")
}

// normalizeContent trims and bounds what the model returned so a stray
// space or a fourth detail does not fail the whole poster.
func normalizeContent(c *posterrender.Content) {
	c.Eyebrow = strings.TrimSpace(c.Eyebrow)
	c.Title = strings.TrimSpace(c.Title)
	c.Summary = strings.TrimSpace(c.Summary)
	c.Action = strings.TrimSpace(c.Action)
	if c.Details == nil {
		c.Details = []posterrender.Detail{}
	}
	kept := c.Details[:0]
	for _, d := range c.Details {
		d.Label, d.Value = strings.TrimSpace(d.Label), strings.TrimSpace(d.Value)
		if d.Label != "" && d.Value != "" && len(kept) < 3 {
			kept = append(kept, d)
		}
	}
	c.Details = kept
}

func (a *App) tenantName(ctx context.Context, tenantID uuid.UUID) (string, error) {
	t, err := models.GetTenantByID(ctx, a.pool, tenantID)
	if err != nil {
		return "", fmt.Errorf("loading tenant: %w", err)
	}
	if t == nil {
		return "", errors.New("tenant not found")
	}
	return t.Name, nil
}
