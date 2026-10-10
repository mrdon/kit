package posters

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/posterrender"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

type searchArg struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

func (a *App) coreSearchPhotos(ctx context.Context, caller *services.Caller, raw json.RawMessage) (*coreResult, error) {
	var in searchArg
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Query) == "" {
		return nil, invalid("query is required")
	}
	hits, err := searchPhotos(ctx, a.pool, caller.TenantID, in.Query, in.Limit)
	if err != nil {
		return nil, err
	}
	if len(hits) == 0 {
		return textResult("No indexed photo matches %q. Try other words, or ask_for_photo if the library has nothing of this.", in.Query)
	}
	var b strings.Builder
	for _, p := range hits {
		b.WriteString(FormatPhoto(&p))
	}
	return textResult("%s", b.String())
}

func (a *App) coreStockPhotos(ctx context.Context, caller *services.Caller, raw json.RawMessage) (*coreResult, error) {
	var in searchArg
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if !a.stockAllowed(ctx, caller) {
		return nil, invalid("stock photos are not allowed in this workspace; an admin can turn them on in Posters settings")
	}
	hits, err := a.pixabay.Search(ctx, in.Query)
	if err != nil {
		return nil, err
	}
	if len(hits) == 0 {
		return textResult("Pixabay has nothing for %q.", in.Query)
	}
	var b strings.Builder
	b.WriteString("Pixabay results (use as <Photo id=\"pixabay:<id>\" />; screen for logos, trademarks and recognisable people; say the source is Pixabay):\n")
	for _, h := range hits {
		fmt.Fprintf(&b, "- pixabay:%s %dx%d, tags: %s, by %s, %s\n", h.ID, h.Width, h.Height, h.Tags, h.User, h.PageURL)
	}
	return textResult("%s", b.String())
}

type askArg struct {
	What string `json:"what"`
}

func (a *App) coreAskForPhoto(ctx context.Context, caller *services.Caller, raw json.RawMessage) (*coreResult, error) {
	var in askArg
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	s, err := getSettings(ctx, a.pool, caller.TenantID)
	if err != nil {
		return nil, err
	}
	folder := "the shared photo folder (an admin sets it in Posters settings)"
	if s.PhotoFolderID != "" {
		folder = "https://drive.google.com/drive/folders/" + s.PhotoFolderID
	}
	return textResult("Tell the user: the library has no photo of %s. Two ways to add one: put it in %s in a subfolder named for the event or place "+
		"(it is picked up within the hour, then someone describes it from Claude Code with the indexing tools), or upload it in this chat "+
		"to use on this poster only (then reference it as <Photo id=\"attachment:<id>\" /> using the attachment id shown in the conversation). "+
		"Do not substitute a photo of something else.", strOr(in.What, "that"), folder)
}

type pendingArg struct {
	Limit int `json:"limit"`
}

func (a *App) coreListPending(ctx context.Context, caller *services.Caller, raw json.RawMessage) (*coreResult, error) {
	var in pendingArg
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if in.Limit <= 0 {
		in.Limit = 48
	}
	photos, err := listPhotos(ctx, a.pool, caller.TenantID, PhotoFilter{Status: PhotoPending, Limit: in.Limit})
	if err != nil {
		return nil, err
	}
	if len(photos) == 0 {
		return textResult("No photos are waiting for descriptions.")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d pending (load the indexing-poster-photos skill, look at them with get_photo_sheet, then index_photos):\n", len(photos))
	for _, p := range photos {
		fmt.Fprintf(&b, "- %s %s/%s %s %dx%d\n", p.ID, p.Folder, p.Filename, p.Orientation, p.Width, p.Height)
	}
	return textResult("%s", b.String())
}

type sheetArg struct {
	IDs []string `json:"ids"`
}

func (a *App) coreSheet(ctx context.Context, caller *services.Caller, raw json.RawMessage) (*coreResult, error) {
	var in sheetArg
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if len(in.IDs) == 0 || len(in.IDs) > 12 {
		return nil, invalid("pass 1 to 12 photo ids")
	}
	ids := make([]uuid.UUID, 0, len(in.IDs))
	for _, s := range in.IDs {
		id, err := parseID(s, "id")
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	photos, err := getPhotos(ctx, a.pool, caller.TenantID, ids)
	if err != nil {
		return nil, err
	}
	brand, err := a.brandFor(ctx, caller.TenantID)
	if err != nil {
		return nil, err
	}
	byID := map[uuid.UUID]Photo{}
	for _, p := range photos {
		byID[p.ID] = p
	}
	items := make([]posterrender.SheetItem, 0, len(ids))
	var legend strings.Builder
	for i, id := range ids {
		p, ok := byID[id]
		if !ok {
			continue
		}
		items = append(items, posterrender.SheetItem{Image: p.Ref(), Label: fmt.Sprintf("%d. %s", i+1, p.Filename)})
		fmt.Fprintf(&legend, "%d. %s (%s/%s)\n", i+1, p.ID, p.Folder, p.Filename)
	}
	if a.renderer == nil {
		return nil, invalid("the renderer is not configured")
	}
	png, err := a.renderer.Sheet(ctx, items, *brand)
	if err != nil {
		return nil, err
	}
	out := &coreResult{Text: "Contact sheet attached. Numbers map to ids:\n" + legend.String()}
	out.png(png)
	return out, nil
}

type photoArg struct {
	ID   string `json:"id"`
	Size int    `json:"size"`
}

func (a *App) coreGetPhoto(ctx context.Context, caller *services.Caller, raw json.RawMessage) (*coreResult, error) {
	var in photoArg
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	id, err := parseID(in.ID, "id")
	if err != nil {
		return nil, err
	}
	p, err := getPhoto(ctx, a.pool, caller.TenantID, id)
	if err != nil {
		return nil, err
	}
	if a.renderer == nil {
		return nil, invalid("the renderer is not configured")
	}
	size := in.Size
	if size <= 0 {
		size = 1024
	}
	jpeg, err := a.renderer.Photo(ctx, p.Ref(), size)
	if err != nil {
		return nil, err
	}
	out := &coreResult{Text: FormatPhoto(p)}
	out.Images = append(out.Images, tools.ToolImage{Mime: "image/jpeg", Data: jpeg})
	return out, nil
}

type indexArg struct {
	Entries []indexEntryArg `json:"entries"`
}

type indexEntryArg struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	FocusX      *float64 `json:"focus_x"`
	FocusY      *float64 `json:"focus_y"`
	Notes       string   `json:"notes"`
}

func (a *App) coreIndexPhotos(ctx context.Context, caller *services.Caller, raw json.RawMessage) (*coreResult, error) {
	var in indexArg
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	if len(in.Entries) == 0 {
		return nil, invalid("entries is required")
	}
	by := strOr(caller.Identity, "mcp")
	done := 0
	var problems []string
	for _, e := range in.Entries {
		if _, err := a.applyIndexEntry(ctx, caller.TenantID, e, by, true); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %s", e.ID, err.Error()))
			continue
		}
		done++
	}
	text := fmt.Sprintf("Indexed %d photo(s).", done)
	if len(problems) > 0 {
		text += "\nNot indexed:\n- " + strings.Join(problems, "\n- ")
	}
	return textResult("%s", text)
}

// applyIndexEntry writes one entry. For a fresh index every field is
// taken as given; for an update, absent fields keep their current value.
func (a *App) applyIndexEntry(ctx context.Context, tenantID uuid.UUID, e indexEntryArg, by string, fresh bool) (*Photo, error) {
	id, err := parseID(e.ID, "id")
	if err != nil {
		return nil, err
	}
	cur, err := getPhoto(ctx, a.pool, tenantID, id)
	if err != nil {
		return nil, err
	}
	entry := IndexEntry{ID: id, Description: cur.Description, Tags: cur.Tags, FocusX: cur.FocusX, FocusY: cur.FocusY, Notes: cur.Notes}
	if fresh || e.Description != "" {
		entry.Description = e.Description
	}
	if fresh || e.Tags != nil {
		entry.Tags = e.Tags
	}
	if e.FocusX != nil {
		entry.FocusX = *e.FocusX
	}
	if e.FocusY != nil {
		entry.FocusY = *e.FocusY
	}
	if fresh || e.Notes != "" {
		entry.Notes = e.Notes
	}
	if strings.TrimSpace(entry.Description) == "" {
		if fresh {
			return nil, invalid("description is required")
		}
		// Focus, tags and notes are hints; without a description the
		// photo stays pending rather than becoming searchable.
		return setPhotoHints(ctx, a.pool, tenantID, entry)
	}
	return indexPhoto(ctx, a.pool, tenantID, entry, by)
}

func (a *App) coreUpdateIndex(ctx context.Context, caller *services.Caller, raw json.RawMessage) (*coreResult, error) {
	var in indexEntryArg
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	p, err := a.applyIndexEntry(ctx, caller.TenantID, in, strOr(caller.Identity, "mcp"), false)
	if err != nil {
		return nil, err
	}
	return textResult("Updated.\n%s", FormatPhoto(p))
}

func (a *App) coreSyncPhotos(ctx context.Context, caller *services.Caller) (*coreResult, error) {
	if !caller.IsAdmin {
		return nil, invalid("only an admin can run the photo sync")
	}
	res, err := a.SyncNow(ctx, caller.TenantID)
	if err != nil {
		return nil, err
	}
	text := fmt.Sprintf("Listed %d photos: %d new, %d inspected, %d removed, %d waiting for descriptions.", res.Listed, res.New, res.Inspected, res.Removed, res.Pending)
	if len(res.Problems) > 0 {
		text += "\nProblems:\n- " + strings.Join(res.Problems, "\n- ")
	}
	return textResult("%s", text)
}
