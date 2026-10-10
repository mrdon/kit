package posters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/services"
)

// coreResult is a tool's outcome: text for the transcript and any renders
// the model should look at.
type coreResult struct {
	Text   string
	Images []toolImage
}

type toolImage struct {
	Mime string
	Data []byte
}

func textResult(format string, args ...any) (*coreResult, error) {
	return &coreResult{Text: fmt.Sprintf(format, args...)}, nil
}

func (r *coreResult) png(data []byte) {
	if len(data) > 0 {
		r.Images = append(r.Images, toolImage{Mime: "image/png", Data: data})
	}
}

// dispatchCore is the one implementation of every poster tool. The agent
// registry and the MCP server are both thin wrappers over it.
func (a *App) dispatchCore(ctx context.Context, caller *services.Caller, name string, raw json.RawMessage) (*coreResult, error) {
	if caller == nil {
		return nil, errors.New("posters: no caller on context")
	}
	if a.pool == nil {
		return nil, errors.New("posters: not initialised")
	}
	switch name {
	case "list_posters":
		return a.coreListPosters(ctx, caller)
	case "get_poster":
		return a.coreGetPoster(ctx, caller, raw)
	case "edit_poster_source":
		return a.coreEditPoster(ctx, caller, raw)
	case "set_poster_fields":
		return a.coreSetFields(ctx, caller, raw)
	case "render_poster_formats":
		return a.coreRenderFormats(ctx, caller, raw)
	case "search_photos":
		return a.coreSearchPhotos(ctx, caller, raw)
	case "find_stock_photos":
		return a.coreStockPhotos(ctx, caller, raw)
	case "ask_for_photo":
		return a.coreAskForPhoto(ctx, caller, raw)
	case "set_poster_on_event":
		return a.coreSetOnEvent(ctx, caller, raw)
	case "update_poster_facts":
		return a.coreUpdateFacts(ctx, caller, raw)
	case "list_templates":
		return a.coreListTemplates(ctx, caller, raw)
	case "get_template":
		return a.coreGetTemplate(ctx, caller, raw)
	case "edit_template_source":
		return a.coreEditTemplate(ctx, caller, raw)
	case "create_template":
		return a.coreCreateTemplate(ctx, caller, raw)
	case "set_template_status":
		return a.coreSetTemplateStatus(ctx, caller, raw)
	case "list_pending_photos":
		return a.coreListPending(ctx, caller, raw)
	case "get_photo_sheet":
		return a.coreSheet(ctx, caller, raw)
	case "get_photo":
		return a.coreGetPhoto(ctx, caller, raw)
	case "index_photos":
		return a.coreIndexPhotos(ctx, caller, raw)
	case "update_photo_index":
		return a.coreUpdateIndex(ctx, caller, raw)
	case "sync_poster_photos":
		return a.coreSyncPhotos(ctx, caller)
	case "generate_poster_options":
		return a.coreGenerate(ctx, caller, raw)
	default:
		return nil, fmt.Errorf("posters: unknown tool %q", name)
	}
}

func decode(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("could not parse input: %w", err)
	}
	return nil
}

func parseID(s, what string) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(s))
	if err != nil {
		return uuid.Nil, invalid("%s must be an id, got %q", what, s)
	}
	return id, nil
}

func callerUser(c *services.Caller) *uuid.UUID {
	if c == nil || c.UserID == uuid.Nil {
		return nil
	}
	id := c.UserID
	return &id
}

type posterArg struct {
	PosterID string `json:"poster_id"`
}

func (a *App) coreListPosters(ctx context.Context, caller *services.Caller) (*coreResult, error) {
	posters, err := listPosters(ctx, a.pool, caller.TenantID)
	if err != nil {
		return nil, err
	}
	if len(posters) == 0 {
		return textResult("No posters yet. Generate options from an event's page in the console, or with generate_poster_options.")
	}
	var b strings.Builder
	for _, p := range posters {
		fmt.Fprintf(&b, "- %s (%s) %s, changed %s\n", strOr(p.Title, "untitled"), p.ID, posterStatus(&p), p.UpdatedAt.Format("2 Jan 15:04"))
	}
	return textResult("%s", b.String())
}

func posterStatus(p *Poster) string {
	switch {
	case p.Stale:
		return "out of date"
	case p.SetVersionID != nil:
		return "on the event"
	default:
		return "draft"
	}
}

func (a *App) coreGetPoster(ctx context.Context, caller *services.Caller, raw json.RawMessage) (*coreResult, error) {
	var in posterArg
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	id, err := parseID(in.PosterID, "poster_id")
	if err != nil {
		return nil, err
	}
	poster, v, err := a.loadPoster(ctx, caller.TenantID, id)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return textResult("Poster %s (%s) has no version yet.", poster.Title, poster.ID)
	}
	return textResult("%s", FormatPoster(poster, v))
}

type editArg struct {
	PosterID string `json:"poster_id"`
	Source   string `json:"source"`
	Summary  string `json:"summary"`
}

func (a *App) coreEditPoster(ctx context.Context, caller *services.Caller, raw json.RawMessage) (*coreResult, error) {
	var in editArg
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	id, err := parseID(in.PosterID, "poster_id")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Source) == "" {
		return nil, invalid("source is required")
	}
	poster, err := getPoster(ctx, a.pool, caller.TenantID, id)
	if err != nil {
		return nil, err
	}
	res, err := a.saveEdit(ctx, caller.TenantID, poster, in.Source, strOr(in.Summary, "Edited"), "agent")
	if err != nil {
		return nil, err
	}
	return editOutcome(res, "Saved as a new version")
}

// editOutcome words a render's result for the model: the problems to fix,
// or the saved version with its picture attached.
func editOutcome(res *EditResult, saved string) (*coreResult, error) {
	if len(res.Problems) > 0 {
		return textResult("Not saved. The render has problems to fix:\n- %s", strings.Join(res.Problems, "\n- "))
	}
	out := &coreResult{Text: fmt.Sprintf("%s %s. Look at the attached render and check it against the brand checklist before replying.", saved, res.Version.ID)}
	if len(res.Warnings) > 0 {
		out.Text += "\nWarnings:\n- " + strings.Join(res.Warnings, "\n- ")
	}
	out.png(res.PNG)
	return out, nil
}

type formatsArg struct {
	PosterID string   `json:"poster_id"`
	Formats  []string `json:"formats"`
}

func (a *App) coreRenderFormats(ctx context.Context, caller *services.Caller, raw json.RawMessage) (*coreResult, error) {
	var in formatsArg
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	id, err := parseID(in.PosterID, "poster_id")
	if err != nil {
		return nil, err
	}
	_, v, err := a.loadPoster(ctx, caller.TenantID, id)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, invalid("this poster has no version yet")
	}
	brand, err := a.brandFor(ctx, caller.TenantID)
	if err != nil {
		return nil, err
	}
	formats := in.Formats
	if len(formats) == 0 {
		formats = sortedKeys(brand.Formats)
	}
	out := &coreResult{}
	var lines []string
	for _, f := range formats {
		if _, ok := brand.Formats[f]; !ok {
			lines = append(lines, fmt.Sprintf("%s: not a format this brand defines (%s)", f, strings.Join(sortedKeys(brand.Formats), ", ")))
			continue
		}
		png, problems, err := a.renderVersion(ctx, caller.TenantID, v, f)
		if err != nil {
			return nil, err
		}
		if len(problems) > 0 {
			lines = append(lines, fmt.Sprintf("%s: %s", f, strings.Join(problems, "; ")))
			continue
		}
		lines = append(lines, fmt.Sprintf("%s: rendered (%dx%d), attached", f, brand.Formats[f].Width, brand.Formats[f].Height))
		if len(out.Images) < 4 {
			out.png(png)
		}
	}
	out.Text = strings.Join(lines, "\n")
	return out, nil
}

type setOnEventArg struct {
	PosterID  string `json:"poster_id"`
	VersionID string `json:"version_id"`
}

func (a *App) coreSetOnEvent(ctx context.Context, caller *services.Caller, raw json.RawMessage) (*coreResult, error) {
	var in setOnEventArg
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
	v := cur
	if in.VersionID != "" {
		vid, err := parseID(in.VersionID, "version_id")
		if err != nil {
			return nil, err
		}
		if v, err = getVersion(ctx, a.pool, caller.TenantID, vid); err != nil {
			return nil, err
		}
		if v.PosterID != poster.ID {
			return nil, ErrNotFound
		}
	}
	if v == nil {
		return nil, invalid("this poster has no version yet")
	}
	if err := a.setOnEvent(ctx, caller.TenantID, caller.UserID, poster, v); err != nil {
		return nil, err
	}
	return textResult("Version %s is now the event's poster.", v.ID)
}

func (a *App) coreUpdateFacts(ctx context.Context, caller *services.Caller, raw json.RawMessage) (*coreResult, error) {
	var in posterArg
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	id, err := parseID(in.PosterID, "poster_id")
	if err != nil {
		return nil, err
	}
	res, changes, err := a.UpdateFacts(ctx, caller.TenantID, id, "agent")
	if err != nil {
		return nil, err
	}
	if len(changes) == 0 && res.Version != nil {
		return textResult("Nothing in the poster states a changed fact; version %s saved unchanged.", res.Version.ID)
	}
	return editOutcome(res, "Facts refreshed ("+strings.Join(changes, "; ")+"); saved as version")
}

type generateArg struct {
	EventID string `json:"event_id"`
	More    bool   `json:"more"`
}

func (a *App) coreGenerate(ctx context.Context, caller *services.Caller, raw json.RawMessage) (*coreResult, error) {
	var in generateArg
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	id, err := parseID(in.EventID, "event_id")
	if err != nil {
		return nil, err
	}
	res, err := a.Generate(ctx, caller.TenantID, caller.UserID, id, in.More, nil)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Poster %s: %d options in batch %s", res.Poster.ID, len(res.Options), res.BatchID)
	if res.NoPhoto {
		b.WriteString(" (no honest photo in the index for this event, so type-only layouts)")
	}
	b.WriteString(".\n")
	for _, o := range res.Options {
		fmt.Fprintf(&b, "- version %s: ground %s, %d photo(s)\n", o.ID, o.Ground, len(o.Photos))
	}
	b.WriteString("Set one with set_poster_on_event(poster_id, version_id).")
	return textResult("%s", b.String())
}
