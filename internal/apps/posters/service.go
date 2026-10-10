package posters

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/events"
	"github.com/mrdon/kit/internal/attachment"
	"github.com/mrdon/kit/internal/posterrender"
)

// This file holds the operations every surface shares: resolving the
// images a poster's source refers to, rendering a version through the
// cache, saving an edit as a new version, and setting a version on its
// event. The console, the agent tools and the MCP tools all call these.

// ErrInvalid wraps a refusal the caller can fix.
var ErrInvalid = errors.New("invalid")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

var (
	uuidPattern    = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
	pixabayPattern = regexp.MustCompile(`pixabay:(\d+)`)
	attachPattern  = regexp.MustCompile(`attachment:([0-9a-f-]{36})`)
)

// imageRefsFor resolves every photo id a source mentions: library photos
// by uuid, stock photos by pixabay:<id> (only when the tenant allows
// stock), and chat uploads by attachment:<id> for this poster only. Ids
// that resolve to nothing are left out; the renderer then reports them as
// unknown photos, which is the right message for the agent.
func (a *App) imageRefsFor(ctx context.Context, tenantID uuid.UUID, source string, allowStock bool) ([]posterrender.ImageRef, error) {
	var refs []posterrender.ImageRef
	seen := map[string]bool{}
	var ids []uuid.UUID
	for _, m := range uuidPattern.FindAllString(source, -1) {
		if id, err := uuid.Parse(m); err == nil && !seen[m] {
			seen[m] = true
			ids = append(ids, id)
		}
	}
	if len(ids) > 0 {
		photos, err := getPhotos(ctx, a.pool, tenantID, ids)
		if err != nil {
			return nil, err
		}
		for _, p := range photos {
			if p.Status != PhotoRemoved && p.C2PA != posterrender.C2PAAI {
				refs = append(refs, p.Ref())
			}
		}
	}
	for _, m := range pixabayPattern.FindAllStringSubmatch(source, -1) {
		if !allowStock || seen[m[0]] {
			continue
		}
		seen[m[0]] = true
		s, err := a.pixabay.Lookup(ctx, m[1])
		if err != nil {
			continue
		}
		refs = append(refs, s.Ref())
	}
	for _, m := range attachPattern.FindAllStringSubmatch(source, -1) {
		if seen[m[0]] {
			continue
		}
		seen[m[0]] = true
		if ref, ok := a.attachmentRef(ctx, tenantID, m[1]); ok {
			refs = append(refs, ref)
		}
	}
	return refs, nil
}

// attachmentRef wraps a chat upload as an inline image for the renderer.
func (a *App) attachmentRef(ctx context.Context, tenantID uuid.UUID, id string) (posterrender.ImageRef, bool) {
	store := a.attachments()
	aid, err := uuid.Parse(id)
	if store == nil || err != nil {
		return posterrender.ImageRef{}, false
	}
	meta, raw, err := store.Load(ctx, tenantID, aid)
	if err != nil || !strings.HasPrefix(meta.Mime, "image/") {
		return posterrender.ImageRef{}, false
	}
	return posterrender.ImageRef{ID: "attachment:" + id, Source: posterrender.SourceInline, Data: base64Encode(raw), Modified: meta.CreatedAt.Format("20060102150405")}, true
}

func (a *App) attachments() *attachment.Service {
	if a.pool == nil || a.enc == nil {
		return nil
	}
	return attachment.NewService(a.pool, a.enc)
}

// renderVersion renders a stored version at a format, through the cache.
func (a *App) renderVersion(ctx context.Context, tenantID uuid.UUID, v *Version, format string) ([]byte, []string, error) {
	if png := a.cache.get(ctx, v.ID.String(), format); png != nil {
		return png, nil, nil
	}
	brand, err := a.brandFor(ctx, tenantID)
	if err != nil {
		return nil, nil, err
	}
	if format == "" {
		format = brand.PortraitFormat
	}
	res, err := a.renderSource(ctx, tenantID, brand, v.Source, format)
	if err != nil {
		return nil, nil, err
	}
	if len(res.Problems) == 0 {
		a.cache.put(ctx, v.ID.String(), format, res.PNG)
	}
	return res.PNG, res.Problems, nil
}

// renderSource renders poster source at a format with the images it names.
func (a *App) renderSource(ctx context.Context, tenantID uuid.UUID, brand *posterrender.Brand, source, format string) (*posterrender.RenderResponse, error) {
	if a.renderer == nil {
		return nil, invalid("the poster renderer is not configured")
	}
	settings, err := getSettings(ctx, a.pool, tenantID)
	if err != nil {
		return nil, err
	}
	refs, err := a.imageRefsFor(ctx, tenantID, source, settings.AllowStockPhotos)
	if err != nil {
		return nil, err
	}
	res, err := a.renderer.Render(ctx, posterrender.RenderRequest{Kind: "poster", Source: source, Format: format, Brand: *brand, Photos: refs})
	var rerr *posterrender.RequestError
	if errors.As(err, &rerr) {
		// A refusal (bad source, missing image) is a problem the caller
		// fixes, not a failure.
		return &posterrender.RenderResponse{Problems: []string{rerr.Message}, Warnings: []string{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("rendering poster: %w", err)
	}
	return res, nil
}

// EditResult is what saving an edit yields: the version when the render
// was clean, else the problems that stopped it.
type EditResult struct {
	Version  *Version `json:"version,omitempty"`
	PNG      []byte   `json:"-"`
	Problems []string `json:"problems"`
	Warnings []string `json:"warnings"`
}

// saveEdit renders new source for a poster and, if it passes, stores it as
// a new version descending from the current one.
func (a *App) saveEdit(ctx context.Context, tenantID uuid.UUID, poster *Poster, source, instruction string, author Author) (*EditResult, error) {
	brand, err := a.brandFor(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	res, err := a.renderSource(ctx, tenantID, brand, source, brand.PortraitFormat)
	if err != nil {
		return nil, err
	}
	out := &EditResult{PNG: res.PNG, Problems: res.Problems, Warnings: res.Warnings}
	if len(res.Problems) > 0 {
		return out, nil
	}
	in := VersionInput{PosterID: poster.ID, ParentID: poster.CurrentVersionID, Source: source, Format: brand.PortraitFormat, Instruction: instruction, Author: author}
	if poster.CurrentVersionID != nil {
		if cur, err := getVersion(ctx, a.pool, tenantID, *poster.CurrentVersionID); err == nil {
			in.TemplateID, in.TemplateVersionID = cur.TemplateID, cur.TemplateVersionID
			in.Content, in.Photos, in.Ground = cur.Content, cur.Photos, cur.Ground
		}
	}
	v, err := insertVersion(ctx, a.pool, tenantID, in)
	if err != nil {
		return nil, err
	}
	if err := setCurrentVersion(ctx, a.pool, tenantID, poster.ID, v.ID); err != nil {
		return nil, err
	}
	a.cache.put(ctx, v.ID.String(), brand.PortraitFormat, res.PNG)
	out.Version = v
	return out, nil
}

// setOnEvent renders the version's portrait, stores it as the event's
// poster through the events app, and records the facts the copy used so a
// later change to the event can flag it.
func (a *App) setOnEvent(ctx context.Context, tenantID, userID uuid.UUID, poster *Poster, v *Version) error {
	if poster.EventID == nil {
		return invalid("this poster is not attached to an event")
	}
	evs := a.eventsService()
	if evs == nil {
		return invalid("the events app is not available")
	}
	ev, err := evs.Get(ctx, tenantID, *poster.EventID)
	if err != nil {
		return fmt.Errorf("loading event: %w", err)
	}
	png, problems, err := a.renderVersion(ctx, tenantID, v, "")
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return invalid("this version does not render cleanly: %s", problems[0])
	}
	store := a.attachments()
	if store == nil {
		return invalid("attachments are not configured")
	}
	att, err := store.Store(ctx, tenantID, userID, "poster-"+ev.Slug+".png", "image/png", png)
	if err != nil {
		return fmt.Errorf("storing poster: %w", err)
	}
	if _, err := evs.Update(ctx, tenantID, ev.ID, events.UpdateParams{HeroAttachmentID: &att.ID}); err != nil {
		return fmt.Errorf("setting poster on event: %w", err)
	}
	facts := factsFor(ev)
	raw, _ := jsonMarshal(facts)
	if err := setOnEvent(ctx, a.pool, tenantID, poster.ID, v.ID, raw, factsHash(facts)); err != nil {
		return err
	}
	return markPicked(ctx, a.pool, tenantID, v.ID)
}

// eventsService is the events app's business rules, indirected so tests
// can run without the events app wired.
func (a *App) eventsService() *events.Service {
	if a.eventsSvc != nil {
		return a.eventsSvc()
	}
	if inst := events.Instance(); inst != nil {
		return inst.Service()
	}
	return nil
}

// loadPoster fetches a poster and its current version.
func (a *App) loadPoster(ctx context.Context, tenantID, id uuid.UUID) (*Poster, *Version, error) {
	p, err := getPoster(ctx, a.pool, tenantID, id)
	if err != nil {
		return nil, nil, err
	}
	if p.CurrentVersionID == nil {
		return p, nil, nil
	}
	v, err := getVersion(ctx, a.pool, tenantID, *p.CurrentVersionID)
	if err != nil {
		return nil, nil, err
	}
	return p, v, nil
}

// activeTemplates lists what the generator may use, with weights from pick
// counts: one pick is worth a lot early, little once the floor dominates,
// and an unpicked template is never weighted to zero.
func (a *App) activeTemplates(ctx context.Context, tenantID uuid.UUID) ([]posterrender.PlannedTemplate, map[string]Template, error) {
	ts, err := listTemplates(ctx, a.pool, tenantID, TemplateActive)
	if err != nil {
		return nil, nil, err
	}
	var out []posterrender.PlannedTemplate
	byID := map[string]Template{}
	for _, t := range ts {
		if t.Hidden || t.CurrentVersionID == nil {
			continue
		}
		v, err := getTemplateVersion(ctx, a.pool, tenantID, *t.CurrentVersionID)
		if err != nil {
			continue
		}
		out = append(out, posterrender.PlannedTemplate{ID: t.ID.String(), Source: v.Source, Weight: 1 + float64(t.Picks), Meta: t.Meta})
		byID[t.ID.String()] = t
	}
	return out, byID, nil
}
