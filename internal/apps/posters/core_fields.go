package posters

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/posterrender"
	"github.com/mrdon/kit/internal/services"
)

// set_poster_fields: quick edits through the inlined constants a picked
// option starts with. A poster the agent has restructured past them gets
// a clear refusal and the suggestion to edit the source instead.

var (
	groundLine = regexp.MustCompile(`\nconst ground = "[^"]*";\n`)
	photosLine = regexp.MustCompile(`\nconst photos = (\[.*?\]);\n`)
)

type fieldsArg struct {
	PosterID string   `json:"poster_id"`
	Layout   string   `json:"layout"`
	Ground   string   `json:"ground"`
	Photo    string   `json:"photo"`
	Zoom     *float64 `json:"zoom"`
	FocusX   *float64 `json:"focus_x"`
	FocusY   *float64 `json:"focus_y"`
}

func (a *App) coreSetFields(ctx context.Context, caller *services.Caller, raw json.RawMessage) (*coreResult, error) {
	var in fieldsArg
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
		return nil, invalid("this poster has no version yet")
	}
	brand, err := a.brandFor(ctx, caller.TenantID)
	if err != nil {
		return nil, err
	}
	source, photos, ground, ok := readInlined(cur.Source)
	if !ok {
		return nil, invalid("this poster no longer carries the inlined content block; use edit_poster_source instead")
	}
	var changes []string
	if in.Ground != "" {
		if _, ok := brand.Grounds[in.Ground]; !ok {
			return nil, invalid("ground must be one of %s", strings.Join(sortedKeys(brand.Grounds), ", "))
		}
		ground = in.Ground
		changes = append(changes, "ground "+in.Ground)
	}
	if photos, changes, err = applyPhotoFields(in, photos, changes); err != nil {
		return nil, err
	}
	if in.Layout != "" {
		if source, changes, err = a.switchLayout(ctx, caller.TenantID, brand, in.Layout, cur, photos, ground, changes); err != nil {
			return nil, err
		}
	} else {
		source = writeInlined(source, photos, ground)
	}
	if len(changes) == 0 {
		return nil, invalid("nothing to change: pass layout, ground, photo, zoom or focus")
	}
	res, err := a.saveEdit(ctx, caller.TenantID, poster, source, "Set "+strings.Join(changes, ", "), "agent")
	if err != nil {
		return nil, err
	}
	return editOutcome(res, "Saved as a new version")
}

// readInlined finds the inlined photos and ground constants.
func readInlined(source string) (string, []posterrender.PhotoUse, string, bool) {
	g := groundLine.FindString(source)
	p := photosLine.FindStringSubmatch(source)
	if g == "" || p == nil {
		return source, nil, "", false
	}
	var photos []posterrender.PhotoUse
	if err := json.Unmarshal([]byte(p[1]), &photos); err != nil {
		return source, nil, "", false
	}
	ground := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(g), `const ground = "`), `";`)
	return source, photos, ground, true
}

// writeInlined rewrites the two constants in place.
func writeInlined(source string, photos []posterrender.PhotoUse, ground string) string {
	pj, _ := json.Marshal(photos)
	source = groundLine.ReplaceAllLiteralString(source, fmt.Sprintf("\nconst ground = %q;\n", ground))
	return photosLine.ReplaceAllLiteralString(source, "\nconst photos = "+string(pj)+";\n")
}

func applyPhotoFields(in fieldsArg, photos []posterrender.PhotoUse, changes []string) ([]posterrender.PhotoUse, []string, error) {
	if in.Photo == "" && in.Zoom == nil && in.FocusX == nil && in.FocusY == nil {
		return photos, changes, nil
	}
	if len(photos) == 0 {
		if in.Photo == "" {
			return nil, nil, invalid("this poster has no photo to adjust; pass photo to add one (and a layout that uses photos)")
		}
		photos = []posterrender.PhotoUse{{FocusX: 0.5, FocusY: 0.5, Zoom: 1}}
	}
	hero := &photos[0]
	if in.Photo != "" {
		hero.ID = strings.TrimSpace(in.Photo)
		changes = append(changes, "photo "+hero.ID)
	}
	if in.Zoom != nil {
		hero.Zoom = clampRange(*in.Zoom, 1, 3)
		changes = append(changes, fmt.Sprintf("zoom %.2f", hero.Zoom))
	}
	if in.FocusX != nil || in.FocusY != nil {
		if in.FocusX != nil {
			hero.FocusX = clamp01(*in.FocusX)
		}
		if in.FocusY != nil {
			hero.FocusY = clamp01(*in.FocusY)
		}
		changes = append(changes, fmt.Sprintf("focus %.2f,%.2f", hero.FocusX, hero.FocusY))
	}
	return photos, changes, nil
}

func clampRange(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// switchLayout re-inlines the poster's content into another template by
// asking the renderer for a one-option batch over that template alone.
func (a *App) switchLayout(ctx context.Context, tenantID uuid.UUID, brand *posterrender.Brand, layout string, cur *Version, photos []posterrender.PhotoUse, ground string, changes []string) (string, []string, error) {
	t, err := a.findTemplate(ctx, tenantID, layout)
	if err != nil {
		return "", nil, err
	}
	if t.CurrentVersionID == nil {
		return "", nil, invalid("template %s has no source", t.Name)
	}
	tv, err := getTemplateVersion(ctx, a.pool, tenantID, *t.CurrentVersionID)
	if err != nil {
		return "", nil, err
	}
	req := posterrender.OptionsRequest{
		Content:   cur.Content,
		Brand:     *brand,
		Format:    brand.PortraitFormat,
		Templates: []posterrender.PlannedTemplate{{ID: t.ID.String(), Source: tv.Source, Weight: 1, Meta: t.Meta}},
		Count:     1,
	}
	if len(photos) > 0 {
		hero := photos[0]
		req.Hero = &hero
		// One resolver for every kind of id (library, pixabay:, attachment:),
		// so a stock or uploaded photo keeps its source and URL.
		ids := make([]string, len(photos))
		for i, p := range photos {
			ids[i] = p.ID
		}
		if req.Photos, err = a.imageRefsFor(ctx, tenantID, strings.Join(ids, " "), true); err != nil {
			return "", nil, err
		}
	}
	res, err := a.renderer.Options(ctx, req)
	if err != nil {
		return "", nil, fmt.Errorf("switching layout: %w", err)
	}
	if len(res.Options) == 0 {
		reason := "the template could not render this content"
		if len(res.Skipped) > 0 {
			reason = res.Skipped[0].Reason
		}
		return "", nil, invalid("%s", reason)
	}
	// The generator chose a ground by its spread; the user's choice wins.
	source := writeInlined(res.Options[0].Source, photos, strOr(ground, res.Options[0].Ground))
	return source, append(changes, "layout "+t.Name), nil
}

// findTemplate resolves a template by id or (case-insensitive) name.
func (a *App) findTemplate(ctx context.Context, tenantID uuid.UUID, ref string) (*Template, error) {
	if id, err := uuid.Parse(strings.TrimSpace(ref)); err == nil {
		return getTemplate(ctx, a.pool, tenantID, id)
	}
	ts, err := listTemplates(ctx, a.pool, tenantID, "")
	if err != nil {
		return nil, err
	}
	for i := range ts {
		if strings.EqualFold(ts[i].Name, strings.TrimSpace(ref)) || strings.EqualFold(ts[i].BuiltinKey, strings.TrimSpace(ref)) {
			return &ts[i], nil
		}
	}
	return nil, invalid("no template named %q; see list_templates", ref)
}
