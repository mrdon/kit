package posters

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/posterrender"
	"github.com/mrdon/kit/internal/services"
)

type templatesArg struct {
	Status string `json:"status"`
}

func (a *App) coreListTemplates(ctx context.Context, caller *services.Caller, raw json.RawMessage) (*coreResult, error) {
	var in templatesArg
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	ts, err := listTemplates(ctx, a.pool, caller.TenantID, TemplateStatus(strings.ToLower(in.Status)))
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	for _, t := range ts {
		b.WriteString(FormatTemplate(&t))
	}
	if b.Len() == 0 {
		return textResult("No templates match.")
	}
	return textResult("%s", b.String())
}

type templateArg struct {
	TemplateID string `json:"template_id"`
}

func (a *App) coreGetTemplate(ctx context.Context, caller *services.Caller, raw json.RawMessage) (*coreResult, error) {
	var in templateArg
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	id, err := parseID(in.TemplateID, "template_id")
	if err != nil {
		return nil, err
	}
	t, err := getTemplate(ctx, a.pool, caller.TenantID, id)
	if err != nil {
		return nil, err
	}
	versions, err := listTemplateVersions(ctx, a.pool, caller.TenantID, id)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString(FormatTemplate(t))
	if len(versions) > 0 {
		b.WriteString("\nVersions (newest first):\n")
		for _, v := range versions {
			fmt.Fprintf(&b, "- %s %s by %s: %s\n", v.ID, v.CreatedAt.Format("2 Jan 15:04"), v.Author, strOr(v.Summary, "(no summary)"))
		}
		fmt.Fprintf(&b, "\nSource:\n```tsx\n%s\n```\n", versions[0].Source)
	}
	return textResult("%s", b.String())
}

// TemplateCheckOutcome is what checking a template's source yields.
type TemplateCheckOutcome struct {
	Meta     *posterrender.TemplateMeta
	Problems []string
	Renders  map[string][]byte
}

// templateCheck runs the activation check with up to three indexed photos
// as sample photos.
func (a *App) templateCheck(ctx context.Context, tenantID uuid.UUID, source string) (*TemplateCheckOutcome, error) {
	if a.renderer == nil {
		return nil, invalid("the renderer is not configured")
	}
	brand, err := a.brandFor(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	photos, err := listPhotos(ctx, a.pool, tenantID, PhotoFilter{Status: PhotoIndexed, Limit: 50})
	if err != nil {
		return nil, err
	}
	var refs []posterrender.ImageRef
	for _, p := range photos {
		if p.Offerable() && len(refs) < 3 {
			refs = append(refs, p.Ref())
		}
	}
	res, err := a.renderer.TemplateCheck(ctx, source, *brand, refs)
	if err != nil {
		var rerr *posterrender.RequestError
		if errorsAs(err, &rerr) {
			return &TemplateCheckOutcome{Problems: []string{rerr.Message}}, nil
		}
		return nil, fmt.Errorf("checking template: %w", err)
	}
	out := &TemplateCheckOutcome{Meta: res.Meta, Renders: res.Renders}
	keys := make([]string, 0, len(res.Problems))
	for k := range res.Problems {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, p := range res.Problems[k] {
			out.Problems = append(out.Problems, k+": "+p)
		}
	}
	return out, nil
}

func checkOutcome(chk *TemplateCheckOutcome, saved string) (*coreResult, error) {
	if len(chk.Problems) > 0 {
		return textResult("Not saved. The template check found problems (format:sample: problem):\n- %s", strings.Join(chk.Problems, "\n- "))
	}
	out := &coreResult{Text: saved + " Look at the attached renders (portrait, story, screen) and check them against the brand checklist before replying."}
	for _, k := range sortedKeys(chk.Renders) {
		out.png(chk.Renders[k])
	}
	return out, nil
}

type editTemplateArg struct {
	TemplateID string `json:"template_id"`
	Source     string `json:"source"`
	Summary    string `json:"summary"`
}

func (a *App) coreEditTemplate(ctx context.Context, caller *services.Caller, raw json.RawMessage) (*coreResult, error) {
	var in editTemplateArg
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	id, err := parseID(in.TemplateID, "template_id")
	if err != nil {
		return nil, err
	}
	t, err := getTemplate(ctx, a.pool, caller.TenantID, id)
	if err != nil {
		return nil, err
	}
	if t.Builtin() {
		return nil, invalid("%s ships with Kit and cannot be edited in place; create_template with its source (parent_template_id=%s) makes a copy you can edit", t.Name, t.ID)
	}
	if strings.TrimSpace(in.Source) == "" {
		return nil, invalid("source is required")
	}
	chk, err := a.templateCheck(ctx, caller.TenantID, in.Source)
	if err != nil {
		return nil, err
	}
	if len(chk.Problems) > 0 {
		return checkOutcome(chk, "")
	}
	v, err := addTemplateVersion(ctx, a.pool, caller.TenantID, t.ID, in.Source, strOr(in.Summary, "Edited"), "agent", *chk.Meta, callerUser(caller))
	if err != nil {
		return nil, err
	}
	return checkOutcome(chk, fmt.Sprintf("Saved template version %s.", v.ID))
}

type createTemplateArg struct {
	Source                string `json:"source"`
	Name                  string `json:"name"`
	Origin                string `json:"origin"`
	SourcePosterID        string `json:"source_poster_id"`
	ReferenceAttachmentID string `json:"reference_attachment_id"`
	ParentTemplateID      string `json:"parent_template_id"`
}

func (a *App) coreCreateTemplate(ctx context.Context, caller *services.Caller, raw json.RawMessage) (*coreResult, error) {
	var in createTemplateArg
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Source) == "" {
		return nil, invalid("source is required")
	}
	origin := strOr(strings.ToLower(in.Origin), "chat")
	if origin != "poster" && origin != "image" && origin != "chat" {
		return nil, invalid("origin must be poster, image or chat")
	}
	chk, err := a.templateCheck(ctx, caller.TenantID, in.Source)
	if err != nil {
		return nil, err
	}
	if len(chk.Problems) > 0 {
		return checkOutcome(chk, "")
	}
	ti := TemplateInput{
		Name: strOr(in.Name, chk.Meta.Name), Description: chk.Meta.Description, Origin: origin, Meta: *chk.Meta,
		Source: in.Source, Summary: "Created", Author: "agent", CreatedBy: callerUser(caller),
	}
	if ti.SourcePosterID, err = optionalID(in.SourcePosterID, "source_poster_id"); err != nil {
		return nil, err
	}
	if ti.ReferenceAttachmentID, err = optionalID(in.ReferenceAttachmentID, "reference_attachment_id"); err != nil {
		return nil, err
	}
	if ti.ParentTemplateID, err = optionalID(in.ParentTemplateID, "parent_template_id"); err != nil {
		return nil, err
	}
	t, err := createTemplate(ctx, a.pool, caller.TenantID, ti)
	if err != nil {
		return nil, err
	}
	return checkOutcome(chk, fmt.Sprintf("Created draft template %q (%s). A person activates it on the templates page.", t.Name, t.ID))
}

//nolint:nilnil // an absent optional id is a value, not a failure
func optionalID(s, what string) (*uuid.UUID, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	id, err := parseID(s, what)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

type statusArg struct {
	TemplateID string `json:"template_id"`
	Status     string `json:"status"`
}

func (a *App) coreSetTemplateStatus(ctx context.Context, caller *services.Caller, raw json.RawMessage) (*coreResult, error) {
	var in statusArg
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	id, err := parseID(in.TemplateID, "template_id")
	if err != nil {
		return nil, err
	}
	if err := a.SetTemplateStatus(ctx, caller.TenantID, id, strings.ToLower(in.Status)); err != nil {
		return nil, err
	}
	return textResult("Template %s is now %s.", id, strings.ToLower(in.Status))
}

// SetTemplateStatus applies a lifecycle change. Built-ins only hide or
// show per tenant; tenant templates move between draft, active, archived.
func (a *App) SetTemplateStatus(ctx context.Context, tenantID, id uuid.UUID, status string) error {
	t, err := getTemplate(ctx, a.pool, tenantID, id)
	if err != nil {
		return err
	}
	if t.Builtin() {
		switch status {
		case "hidden", "archived":
			return setTemplateHidden(ctx, a.pool, tenantID, id, true)
		case "visible", "active":
			return setTemplateHidden(ctx, a.pool, tenantID, id, false)
		}
		return invalid("a built-in template can be hidden or visible, not %q", status)
	}
	switch TemplateStatus(status) {
	case TemplateDraft, TemplateActive, TemplateArchived:
		return setTemplateStatus(ctx, a.pool, tenantID, id, TemplateStatus(status))
	}
	return invalid("status must be draft, active or archived")
}
