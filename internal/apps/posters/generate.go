package posters

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/events"
	"github.com/mrdon/kit/internal/posterrender"
)

func base64Encode(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

// Generating options: copy from the event (Sonnet), a hero photo from the
// index (search, then Sonnet over the top hits), then the renderer's
// deterministic spread over the active templates. Every option is stored
// as a version in one batch; the renders go to the cache.

// optionCount is how many options a batch aims for.
const optionCount = 7

// Progress reports a stage to whoever is watching (the drawer's SSE).
type Progress func(stage string, data map[string]any)

// GenerateResult is a finished batch.
type GenerateResult struct {
	Poster  *Poster   `json:"poster"`
	BatchID uuid.UUID `json:"batch_id"`
	Options []Version `json:"options"`
	Skipped int       `json:"skipped"`
	// NoPhoto says the index had nothing honest for this event, so only
	// type-only layouts were offered.
	NoPhoto bool `json:"no_photo"`
}

// Generate makes a batch of options for an event. With more set, the copy
// and hero of the latest batch are reused and templates already shown are
// avoided, so "more options" costs no model calls.
func (a *App) Generate(ctx context.Context, tenantID, userID uuid.UUID, eventID uuid.UUID, more bool, progress Progress) (*GenerateResult, error) {
	if progress == nil {
		progress = func(string, map[string]any) {}
	}
	if a.renderer == nil {
		return nil, invalid("the poster renderer is not configured")
	}
	brand, err := a.brandFor(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	evs := a.eventsService()
	if evs == nil {
		return nil, invalid("the events app is not available")
	}
	ev, err := evs.Get(ctx, tenantID, eventID)
	if err != nil {
		if errors.Is(err, events.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("loading event: %w", err)
	}
	poster, err := a.posterForEventOrCreate(ctx, tenantID, userID, ev)
	if err != nil {
		return nil, err
	}
	content, hero, heroPhoto, err := a.copyAndPhoto(ctx, tenantID, poster, ev, brand, more, progress)
	if err != nil {
		return nil, err
	}
	progress("rendering", nil)
	templates, byID, err := a.activeTemplates(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if len(templates) == 0 {
		return nil, invalid("no active templates; activate at least one on the templates page")
	}
	req := posterrender.OptionsRequest{Content: *content, Brand: *brand, Format: brand.PortraitFormat, Templates: templates, Count: optionCount, Hero: hero}
	if heroPhoto != nil {
		if req.Photos, err = a.photoSet(ctx, tenantID, heroPhoto); err != nil {
			return nil, err
		}
	}
	if more {
		if req.Exclude, err = shownTemplateIDs(ctx, a.pool, tenantID, poster.ID); err != nil {
			return nil, err
		}
	}
	res, err := a.renderer.Options(ctx, req)
	if err != nil {
		var rerr *posterrender.RequestError
		if errors.As(err, &rerr) {
			return nil, invalid("%s", rerr.Message)
		}
		return nil, fmt.Errorf("generating options: %w", err)
	}
	return a.storeBatch(ctx, tenantID, poster, brand, content, res, byID, hero == nil)
}

func (a *App) posterForEventOrCreate(ctx context.Context, tenantID, userID uuid.UUID, ev *events.Event) (*Poster, error) {
	poster, err := posterForEvent(ctx, a.pool, tenantID, ev.ID)
	if err != nil {
		return nil, err
	}
	if poster != nil {
		return poster, nil
	}
	return createPoster(ctx, a.pool, tenantID, &ev.ID, ev.Title, &userID)
}

// copyAndPhoto gets the content and hero for a batch, from the latest batch
// when more is set, else from the model.
func (a *App) copyAndPhoto(ctx context.Context, tenantID uuid.UUID, poster *Poster, ev *events.Event, brand *posterrender.Brand, more bool, progress Progress) (*posterrender.Content, *posterrender.PhotoUse, *Photo, error) {
	if more {
		if c, hero, photo, ok := a.lastBatchInputs(ctx, tenantID, poster); ok {
			return c, hero, photo, nil
		}
	}
	progress("copy", nil)
	content, err := a.writeCopy(ctx, tenantID, ev, brand)
	if err != nil {
		return nil, nil, nil, err
	}
	progress("photo", nil)
	photo, hero, err := a.pickPhoto(ctx, tenantID, ev)
	if err != nil {
		return nil, nil, nil, err
	}
	return content, hero, photo, nil
}

// lastBatchInputs reads the content and hero back from the poster's most
// recent option, so another batch needs no model.
func (a *App) lastBatchInputs(ctx context.Context, tenantID uuid.UUID, poster *Poster) (*posterrender.Content, *posterrender.PhotoUse, *Photo, bool) {
	versions, err := listVersions(ctx, a.pool, tenantID, poster.ID, true)
	if err != nil {
		return nil, nil, nil, false
	}
	for _, v := range versions {
		if v.BatchID == nil || v.Content.Title == "" {
			continue
		}
		c := v.Content
		var hero *posterrender.PhotoUse
		var photo *Photo
		// The hero is the first photo of any photo-bearing option in the batch.
		batch, _ := listBatch(ctx, a.pool, tenantID, *v.BatchID)
		for _, b := range batch {
			if len(b.Photos) > 0 {
				if id, err := uuid.Parse(b.Photos[0].ID); err == nil {
					if p, err := getPhoto(ctx, a.pool, tenantID, id); err == nil {
						use := b.Photos[0]
						hero, photo = &use, p
					}
				}
				break
			}
		}
		return &c, hero, photo, true
	}
	return nil, nil, nil, false
}

// photoSet is the hero's folder: the generator borrows siblings from the
// same set, never from the library root.
func (a *App) photoSet(ctx context.Context, tenantID uuid.UUID, hero *Photo) ([]posterrender.ImageRef, error) {
	refs := []posterrender.ImageRef{hero.Ref()}
	if hero.Folder == "" {
		return refs, nil
	}
	siblings, err := listPhotos(ctx, a.pool, tenantID, PhotoFilter{Status: PhotoIndexed, Folder: hero.Folder})
	if err != nil {
		return nil, err
	}
	for _, s := range siblings {
		if s.ID != hero.ID && s.Offerable() {
			refs = append(refs, s.Ref())
		}
	}
	return refs, nil
}

// storeBatch saves the options as versions sharing a batch id and warms
// the render cache with their PNGs.
func (a *App) storeBatch(ctx context.Context, tenantID uuid.UUID, poster *Poster, brand *posterrender.Brand, content *posterrender.Content, res *posterrender.OptionsResponse, byID map[string]Template, noPhoto bool) (*GenerateResult, error) {
	if len(res.Options) == 0 {
		reason := "no template could render this content inside the safe margins"
		if len(res.Skipped) > 0 {
			reason = res.Skipped[0].Reason
		}
		return nil, invalid("no options could be made: %s", reason)
	}
	batch := uuid.New()
	out := &GenerateResult{Poster: poster, BatchID: batch, Skipped: len(res.Skipped), NoPhoto: noPhoto, Options: []Version{}}
	for _, o := range res.Options {
		in := VersionInput{PosterID: poster.ID, BatchID: &batch, Source: o.Source, Content: *content, Photos: o.Photos, Ground: o.Ground, Format: brand.PortraitFormat, Author: "system"}
		if t, ok := byID[o.TemplateID]; ok {
			in.TemplateID, in.TemplateVersionID = uuidPtr(t.ID), t.CurrentVersionID
		}
		v, err := insertVersion(ctx, a.pool, tenantID, in)
		if err != nil {
			return nil, err
		}
		a.cache.put(ctx, v.ID.String(), brand.PortraitFormat, o.PNG)
		out.Options = append(out.Options, *v)
	}
	if poster.CurrentVersionID == nil {
		if err := setCurrentVersion(ctx, a.pool, tenantID, poster.ID, out.Options[0].ID); err != nil {
			return nil, err
		}
		poster.CurrentVersionID = &out.Options[0].ID
	}
	if len(res.Skipped) > 0 {
		slog.Info("posters: options skipped", "poster_id", poster.ID, "skipped", len(res.Skipped), "first", res.Skipped[0].Reason)
	}
	return out, nil
}

// Pick makes an option the poster's current version and the one on the
// event. A pick is what template statistics count.
func (a *App) Pick(ctx context.Context, tenantID, userID, posterID, versionID uuid.UUID) (*Poster, error) {
	poster, err := getPoster(ctx, a.pool, tenantID, posterID)
	if err != nil {
		return nil, err
	}
	v, err := getVersion(ctx, a.pool, tenantID, versionID)
	if err != nil {
		return nil, err
	}
	if v.PosterID != poster.ID {
		return nil, ErrNotFound
	}
	if err := a.setOnEvent(ctx, tenantID, userID, poster, v); err != nil {
		return nil, err
	}
	return getPoster(ctx, a.pool, tenantID, posterID)
}
