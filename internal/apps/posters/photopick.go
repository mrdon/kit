package posters

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/apps/events"
	"github.com/mrdon/kit/internal/posterrender"
)

// pickCandidates is how many search hits the model sees.
const pickCandidates = 5

type pickCandidate struct {
	ID          string
	Orientation string
	Folder      string
	FocusX      float64
	FocusY      float64
	Description string
	Tags        string
	Notes       string
}

// pickPhoto searches the index with the event's words and asks Sonnet to
// choose a hero among the top hits, or none. The returned use carries the
// model's focus point; the photo is the row it chose.
func (a *App) pickPhoto(ctx context.Context, tenantID uuid.UUID, e *events.Event) (*Photo, *posterrender.PhotoUse, error) {
	query := strings.Join(nonEmpty(e.Title, e.Summary, strings.Join(e.Labels, " ")), " ")
	hits, err := searchPhotos(ctx, a.pool, tenantID, query, pickCandidates)
	if err != nil {
		return nil, nil, err
	}
	if len(hits) == 0 {
		// A broader net: the description alone, then anything indexed in
		// the most recent folders. Honesty about the subject is the
		// model's job; this just gives it something to say no to.
		hits, err = searchPhotos(ctx, a.pool, tenantID, firstWords(e.Description, 12), pickCandidates)
		if err != nil {
			return nil, nil, err
		}
	}
	if len(hits) == 0 {
		return nil, nil, nil
	}
	cands := make([]pickCandidate, 0, len(hits))
	byID := map[string]Photo{}
	for _, h := range hits {
		byID[h.ID.String()] = h
		cands = append(cands, pickCandidate{
			ID: h.ID.String(), Orientation: h.Orientation, Folder: h.Folder, FocusX: h.FocusX, FocusY: h.FocusY,
			Description: h.Description, Tags: strings.Join(h.Tags, ", "), Notes: h.Notes,
		})
	}
	var pick struct {
		ID     *string `json:"id"`
		FocusX float64 `json:"focus_x"`
		FocusY float64 `json:"focus_y"`
		Why    string  `json:"why"`
	}
	system := mustRender("system_photo_pick.tmpl", nil)
	user := mustRender("user_photo_pick.tmpl", map[string]any{"Event": factsText(factsFor(e)), "Candidates": cands})
	if err := askJSON(ctx, a.llm, system, user, nil, &pick); err != nil {
		return nil, nil, fmt.Errorf("choosing a photo: %w", err)
	}
	if pick.ID == nil || *pick.ID == "" || *pick.ID == "null" {
		slog.Info("posters: no honest photo for event", "event", e.Title, "why", pick.Why)
		return nil, nil, nil
	}
	p, ok := byID[*pick.ID]
	if !ok {
		return nil, nil, nil
	}
	use := p.Use()
	if pick.FocusX > 0 || pick.FocusY > 0 {
		use.FocusX, use.FocusY = clamp01(pick.FocusX), clamp01(pick.FocusY)
	}
	return &p, &use, nil
}

func nonEmpty(ss ...string) []string {
	out := ss[:0]
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

func firstWords(s string, n int) string {
	words := strings.Fields(s)
	if len(words) > n {
		words = words[:n]
	}
	return strings.Join(words, " ")
}
