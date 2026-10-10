package posters

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/events"
	"github.com/mrdon/kit/internal/attachment"
	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/posterrender"
)

// templateView is a template as the templates page shows it.
type templateView struct {
	Template
	Edits []string `json:"edits"`
}

func (a *App) handleListTemplates(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	ts, err := listTemplates(r.Context(), a.pool, caller.TenantID, TemplateStatus(r.URL.Query().Get("status")))
	if err != nil {
		a.httpErr(w, err)
		return
	}
	out := make([]templateView, 0, len(ts))
	for _, t := range ts {
		edits, _ := templateEdits(r.Context(), a.pool, caller.TenantID, t.ID, 3)
		if edits == nil {
			edits = []string{}
		}
		out = append(out, templateView{Template: t, Edits: edits})
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": out})
}

type createTemplateBody struct {
	Name             string `json:"name"`
	ParentTemplateID string `json:"parent_template_id"`
}

// handleCreateTemplate starts a draft: a copy of a parent (duplicate and
// edit) or the built-in Type layout as a blank slate for template chat.
func (a *App) handleCreateTemplate(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	var body createTemplateBody
	if !readBody(w, r, &body) {
		return
	}
	in := TemplateInput{Origin: "chat", Author: "user", Summary: "Created", CreatedBy: callerUser(caller)}
	if body.ParentTemplateID != "" {
		pid, err := uuid.Parse(body.ParentTemplateID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid parent_template_id")
			return
		}
		parent, err := getTemplate(r.Context(), a.pool, caller.TenantID, pid)
		if err != nil {
			a.httpErr(w, err)
			return
		}
		if parent.CurrentVersionID == nil {
			writeErr(w, http.StatusBadRequest, "the parent template has no source")
			return
		}
		pv, err := getTemplateVersion(r.Context(), a.pool, caller.TenantID, *parent.CurrentVersionID)
		if err != nil {
			a.httpErr(w, err)
			return
		}
		in.Source, in.Meta, in.ParentTemplateID = pv.Source, parent.Meta, &pid
		in.Name, in.Description = strOr(body.Name, parent.Name+" copy"), parent.Description
		in.Summary = "Duplicated from " + parent.Name
	} else {
		bs, err := Builtins()
		if err != nil || len(bs) == 0 {
			a.httpErr(w, err)
			return
		}
		starter := bs[len(bs)-1] // "type": the simplest layout
		in.Source, in.Meta = starter.Source, starter.Meta
		in.Name, in.Description = strOr(body.Name, "New template"), "Describe the layout you want in the chat."
	}
	t, err := createTemplate(r.Context(), a.pool, caller.TenantID, in)
	if err != nil {
		a.httpErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"template": t})
}

func (a *App) handleGetTemplate(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	t, err := getTemplate(r.Context(), a.pool, caller.TenantID, id)
	if err != nil {
		a.httpErr(w, err)
		return
	}
	versions, err := listTemplateVersions(r.Context(), a.pool, caller.TenantID, id)
	if err != nil {
		a.httpErr(w, err)
		return
	}
	edits, _ := templateEdits(r.Context(), a.pool, caller.TenantID, id, 10)
	formats := []string{}
	if brand, err := a.brandFor(r.Context(), caller.TenantID); err == nil {
		formats = sortedKeys(brand.Formats)
	}
	writeJSON(w, http.StatusOK, map[string]any{"template": templateView{Template: *t, Edits: edits}, "versions": versions, "formats": formats})
}

type statusBody struct {
	Status string `json:"status"`
}

func (a *App) handleTemplateStatus(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var body statusBody
	if !readBody(w, r, &body) {
		return
	}
	if err := a.SetTemplateStatus(r.Context(), caller.TenantID, id, strings.ToLower(body.Status)); err != nil {
		a.httpErr(w, err)
		return
	}
	t, err := getTemplate(r.Context(), a.pool, caller.TenantID, id)
	if err != nil {
		a.httpErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"template": t})
}

type nameBody struct {
	Name string `json:"name"`
}

func (a *App) handleRenameTemplate(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var body nameBody
	if !readBody(w, r, &body) || strings.TrimSpace(body.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := renameTemplate(r.Context(), a.pool, caller.TenantID, id, strings.TrimSpace(body.Name)); err != nil {
		a.httpErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRollbackTemplate makes an older version current by saving it
// again as a new version, so history stays linear and nothing is lost.
func (a *App) handleRollbackTemplate(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var body versionBody
	if !readBody(w, r, &body) {
		return
	}
	vid, err := uuid.Parse(body.VersionID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "version_id is required")
		return
	}
	v, err := getTemplateVersion(r.Context(), a.pool, caller.TenantID, vid)
	if err != nil || v.TemplateID != id {
		writeErr(w, http.StatusNotFound, "version not found")
		return
	}
	t, err := getTemplate(r.Context(), a.pool, caller.TenantID, id)
	if err != nil {
		a.httpErr(w, err)
		return
	}
	nv, err := addTemplateVersion(r.Context(), a.pool, caller.TenantID, id, v.Source, "Rolled back to "+v.CreatedAt.Format("2 Jan 15:04"), "user", t.Meta, callerUser(caller))
	if err != nil {
		a.httpErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"version": nv})
}

// handleRenderTemplate renders a template with real content: the event
// named by ?event_id, else the next upcoming event, else sample content.
// Content comes from the event's poster when it has one (the model wrote
// that), else a plain rendering of the facts. No model call here.
func (a *App) handleRenderTemplate(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	t, err := getTemplate(r.Context(), a.pool, caller.TenantID, id)
	if err != nil {
		a.httpErr(w, err)
		return
	}
	if t.CurrentVersionID == nil {
		writeErr(w, http.StatusUnprocessableEntity, "template has no source")
		return
	}
	tv, err := getTemplateVersion(r.Context(), a.pool, caller.TenantID, *t.CurrentVersionID)
	if err != nil {
		a.httpErr(w, err)
		return
	}
	brand, err := a.brandFor(r.Context(), caller.TenantID)
	if err != nil {
		a.httpErr(w, err)
		return
	}
	format := strOr(r.URL.Query().Get("format"), brand.PortraitFormat)
	props, refs, err := a.previewProps(r, caller.TenantID, t, brand)
	if err != nil {
		a.httpErr(w, err)
		return
	}
	if a.renderer == nil {
		writeErr(w, http.StatusServiceUnavailable, "the renderer is not configured")
		return
	}
	res, err := a.renderer.Render(r.Context(), posterrender.RenderRequest{Kind: "template", Source: tv.Source, Format: format, Brand: *brand, Photos: refs, Props: props})
	if err != nil {
		var rerr *posterrender.RequestError
		if errors.As(err, &rerr) {
			writeErr(w, http.StatusUnprocessableEntity, rerr.Message)
			return
		}
		a.httpErr(w, err)
		return
	}
	if len(res.Problems) > 0 {
		writeErr(w, http.StatusUnprocessableEntity, strings.Join(res.Problems, "; "))
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=300")
	http.ServeContent(w, r, "template.png", time.Time{}, strings.NewReader(string(res.PNG)))
}

// previewProps builds the content and photos a template preview uses.
func (a *App) previewProps(r *http.Request, tenantID uuid.UUID, t *Template, brand *posterrender.Brand) (*posterrender.TemplateProps, []posterrender.ImageRef, error) {
	ctx := r.Context()
	ground := strOr(r.URL.Query().Get("ground"), sortedKeys(brand.Grounds)[0])
	accent := sortedKeys(brand.Accents)[0]
	props := &posterrender.TemplateProps{Ground: ground, Accent: accent, Photos: []posterrender.PhotoUse{}}
	ev := a.previewEvent(ctx, tenantID, r.URL.Query().Get("event_id"))
	var refs []posterrender.ImageRef
	if ev != nil {
		props.Content = previewContent(ev)
		if poster, _ := posterForEvent(ctx, a.pool, tenantID, ev.ID); poster != nil && poster.CurrentVersionID != nil {
			if v, err := getVersion(ctx, a.pool, tenantID, *poster.CurrentVersionID); err == nil && v.Content.Title != "" {
				props.Content = v.Content
				for _, p := range v.Photos {
					if id, err := uuid.Parse(p.ID); err == nil {
						if ph, err := getPhoto(ctx, a.pool, tenantID, id); err == nil && ph.Offerable() {
							props.Photos = append(props.Photos, p)
							refs = append(refs, ph.Ref())
						}
					}
				}
			}
		}
	} else {
		props.Content = posterrender.Content{Eyebrow: "Every Wednesday", Title: "Trivia night", Summary: "Free to play, no sign-up. Turn up with a team, or come on your own and join one.", Details: []posterrender.Detail{{Label: "When", Value: "6:30 to 8:30pm"}}}
	}
	for len(props.Photos) < t.Meta.Photos.Min {
		photos, _ := listPhotos(ctx, a.pool, tenantID, PhotoFilter{Status: PhotoIndexed, Limit: 20})
		added := false
		for _, ph := range photos {
			if ph.Offerable() && !hasPhoto(props.Photos, ph.ID.String()) {
				props.Photos = append(props.Photos, ph.Use())
				refs = append(refs, ph.Ref())
				added = true
				if len(props.Photos) >= t.Meta.Photos.Min {
					break
				}
			}
		}
		if !added {
			return nil, nil, invalid("this template needs %d photo(s) and the library has none indexed yet", t.Meta.Photos.Min)
		}
	}
	return props, refs, nil
}

func hasPhoto(uses []posterrender.PhotoUse, id string) bool {
	for _, u := range uses {
		if u.ID == id {
			return true
		}
	}
	return false
}

// previewEvent is the event named by id, else the next upcoming one.
func (a *App) previewEvent(ctx context.Context, tenantID uuid.UUID, eventID string) *events.Event {
	evs := a.eventsService()
	if evs == nil {
		return nil
	}
	if id, err := uuid.Parse(eventID); err == nil {
		if ev, err := evs.Get(ctx, tenantID, id); err == nil {
			return ev
		}
	}
	now := time.Now()
	list, err := evs.List(ctx, tenantID, events.ListFilter{From: &now, ExcludeCancelled: true, Limit: 20})
	if err != nil || len(list) == 0 {
		return nil
	}
	return &list[0]
}

// previewContent is the deterministic copy for a template preview when the
// event has no generated poster yet: the facts, plainly.
func previewContent(ev *events.Event) posterrender.Content {
	loc := ev.Loc()
	start := ev.StartsAt.In(loc)
	eyebrow := start.Format("Mon Jan 2")
	if ev.Repeats() {
		if words := ev.CadenceWords(); words != "" {
			eyebrow = capitalize(firstWords(words, 3))
		}
	}
	c := posterrender.Content{Eyebrow: eyebrow, Title: ev.Title, Summary: truncate(firstSentence(ev.Summary), 140), Details: []posterrender.Detail{}}
	if !ev.AllDay {
		when := strings.ToLower(start.Format("3:04pm"))
		when = strings.Replace(when, ":00", "", 1)
		if ev.EndsAt != nil {
			end := strings.Replace(strings.ToLower(ev.EndsAt.In(loc).Format("3:04pm")), ":00", "", 1)
			when += " to " + end
		}
		c.Details = append(c.Details, posterrender.Detail{Label: "When", Value: when})
	}
	if ev.PriceCents != nil {
		price := "Free"
		if *ev.PriceCents > 0 {
			price = formatMoney(*ev.PriceCents, ev.Currency)
		}
		c.Details = append(c.Details, posterrender.Detail{Label: "Price", Value: price})
	}
	return c
}

func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, ".!?"); i > 0 && i < len(s)-1 {
		return s[:i+1]
	}
	return s
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// handleTemplateReference serves the reference image a template came from.
func (a *App) handleTemplateReference(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	t, err := getTemplate(r.Context(), a.pool, caller.TenantID, id)
	if err != nil || t.ReferenceAttachmentID == nil {
		http.NotFound(w, r)
		return
	}
	store := a.attachments()
	if store == nil {
		http.NotFound(w, r)
		return
	}
	meta, raw, err := store.Load(r.Context(), caller.TenantID, *t.ReferenceAttachmentID)
	if err != nil {
		if errors.Is(err, attachment.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		a.httpErr(w, err)
		return
	}
	if !strings.HasPrefix(meta.Mime, "image/") || meta.Mime == "image/svg+xml" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", meta.Mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(raw)
}

func (a *App) handleSaveTemplate(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	t, err := a.SaveAsTemplate(r.Context(), caller.TenantID, id, callerUser(caller))
	if err != nil {
		a.httpErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"template": t})
}
