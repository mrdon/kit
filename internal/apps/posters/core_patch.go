package posters

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mrdon/kit/internal/services"
)

// patch_poster_source and patch_template_source: an edit as a list of
// find-and-replace pairs rather than the whole file. Most chat edits touch
// a few lines, and the whole-file rewrite was most of what a turn cost in
// output tokens. The result goes through exactly the checks and saves the
// whole-file tools use.

type patchEdit struct {
	Find    string `json:"find"`
	Replace string `json:"replace"`
}

type patchArg struct {
	PosterID   string      `json:"poster_id"`
	TemplateID string      `json:"template_id"`
	Edits      []patchEdit `json:"edits"`
	Summary    string      `json:"summary"`
}

// applyPatches applies the edits in order. Each find must appear exactly
// once in the source as it stands when that edit is reached, so an edit
// cannot land in the wrong place or fan out; the error says which edit
// failed and how many matches it had, so the model can widen or narrow
// the text it quoted.
func applyPatches(source string, edits []patchEdit) (string, error) {
	if len(edits) == 0 {
		return "", invalid("edits is empty")
	}
	for i, e := range edits {
		if e.Find == "" {
			return "", invalid("edit %d: find is empty", i+1)
		}
		switch n := strings.Count(source, e.Find); n {
		case 1:
			source = strings.Replace(source, e.Find, e.Replace, 1)
		case 0:
			return "", invalid("edit %d: the text to find is not in the source (quote it exactly, whitespace included): %q", i+1, snippet(e.Find))
		default:
			return "", invalid("edit %d: the text to find appears %d times; include more surrounding lines so it matches once: %q", i+1, n, snippet(e.Find))
		}
	}
	return source, nil
}

func snippet(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", "⏎")
	if len(s) > 80 {
		return s[:77] + "..."
	}
	return s
}

func (a *App) corePatchPoster(ctx context.Context, caller *services.Caller, raw json.RawMessage) (*coreResult, error) {
	var in patchArg
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	id, err := parseID(in.PosterID, "poster_id")
	if err != nil {
		return nil, err
	}
	poster, cur, err := a.loadPoster(ctx, caller.TenantID, id)
	if err != nil {
		return nil, err
	}
	if cur == nil {
		return nil, invalid("this poster has no version to patch yet")
	}
	source, err := applyPatches(cur.Source, in.Edits)
	if err != nil {
		return nil, err
	}
	res, err := a.saveEdit(ctx, caller.TenantID, poster, source, strOr(in.Summary, "Edited"), AuthorAgent)
	if err != nil {
		return nil, err
	}
	return editOutcome(res, fmt.Sprintf("Applied %d edit(s) and saved as a new version", len(in.Edits)))
}

func (a *App) corePatchTemplate(ctx context.Context, caller *services.Caller, raw json.RawMessage) (*coreResult, error) {
	var in patchArg
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
	if t.CurrentVersionID == nil {
		return nil, invalid("this template has no source to patch yet")
	}
	tv, err := getTemplateVersion(ctx, a.pool, caller.TenantID, *t.CurrentVersionID)
	if err != nil {
		return nil, err
	}
	source, err := applyPatches(tv.Source, in.Edits)
	if err != nil {
		return nil, err
	}
	chk, err := a.templateCheck(ctx, caller.TenantID, source)
	if err != nil {
		return nil, err
	}
	if len(chk.Problems) > 0 {
		return checkOutcome(chk, "")
	}
	v, err := addTemplateVersion(ctx, a.pool, caller.TenantID, t.ID, source, strOr(in.Summary, "Edited"), AuthorAgent, *chk.Meta, callerUser(caller))
	if err != nil {
		return nil, err
	}
	return checkOutcome(chk, fmt.Sprintf("Applied %d edit(s); saved template version %s.", len(in.Edits), v.ID))
}
