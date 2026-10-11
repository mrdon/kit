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

// pickResult is the hero a batch uses: a library photo, or a stock photo
// when the library had nothing honest and the workspace allows stock.
type pickResult struct {
	photo *Photo
	stock *StockPhoto
	use   *posterrender.PhotoUse
}

// pickPhoto searches the index with the event's words and asks Sonnet to
// choose a hero among the top hits, or none. The returned use carries the
// model's focus point. With no library candidate and stock switched on,
// the same question is asked over Pixabay hits instead.
func (a *App) pickPhoto(ctx context.Context, tenantID uuid.UUID, e *events.Event) (*pickResult, error) {
	query := strings.Join(nonEmpty(e.Title, e.Summary, strings.Join(e.Labels, " ")), " ")
	hits, err := searchPhotos(ctx, a.pool, tenantID, query, pickCandidates)
	if err != nil {
		return nil, err
	}
	if len(hits) == 0 {
		// A broader net: the description alone, then anything indexed in
		// the most recent folders. Honesty about the subject is the
		// model's job; this just gives it something to say no to.
		hits, err = searchPhotos(ctx, a.pool, tenantID, firstWords(e.Description, 12), pickCandidates)
		if err != nil {
			return nil, err
		}
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
	stockByID := map[string]StockPhoto{}
	if len(cands) == 0 {
		stock, err := a.stockCandidates(ctx, tenantID, query)
		if err != nil {
			slog.Warn("posters: stock search for a hero", "error", err)
		}
		for _, s := range stock {
			stockByID[pixabayPrefix+s.ID] = s
			cands = append(cands, pickCandidate{
				ID: pixabayPrefix + s.ID, Orientation: orientationOf(s.Width, s.Height), Folder: "pixabay", FocusX: 0.5, FocusY: 0.5,
				Description: s.Tags, Notes: "a stock photo from Pixabay; only if it shows this event's subject with no logo, trademark or recognisable person",
			})
		}
	}
	if len(cands) == 0 {
		return nil, nil //nolint:nilnil // no hero is a normal outcome
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
		return nil, fmt.Errorf("choosing a photo: %w", err)
	}
	if pick.ID == nil || *pick.ID == "" || *pick.ID == "null" {
		slog.Info("posters: no honest photo for event", "event", e.Title, "why", pick.Why)
		return nil, nil //nolint:nilnil // no hero is a normal outcome
	}
	focus := func(use posterrender.PhotoUse) *posterrender.PhotoUse {
		if pick.FocusX > 0 || pick.FocusY > 0 {
			use.FocusX, use.FocusY = clamp01(pick.FocusX), clamp01(pick.FocusY)
		}
		return &use
	}
	if s, ok := stockByID[*pick.ID]; ok {
		return &pickResult{stock: &s, use: focus(posterrender.PhotoUse{ID: pixabayPrefix + s.ID, FocusX: 0.5, FocusY: 0.5, Zoom: 1})}, nil
	}
	p, ok := byID[*pick.ID]
	if !ok {
		return nil, nil //nolint:nilnil // the model named something it was not offered
	}
	return &pickResult{photo: &p, use: focus(p.Use())}, nil
}

// stockCandidates is the Pixabay search behind the hero pick, empty when
// the workspace has stock off or no key.
func (a *App) stockCandidates(ctx context.Context, tenantID uuid.UUID, query string) ([]StockPhoto, error) {
	s, err := getSettings(ctx, a.pool, tenantID)
	if err != nil || !s.AllowStockPhotos {
		return nil, err
	}
	key, err := a.pixabayKey(ctx, tenantID)
	if err != nil || key == "" {
		return nil, err
	}
	hits, err := a.pixabay.Search(ctx, key, firstWords(query, 6))
	if err != nil {
		return nil, err
	}
	if len(hits) > pickCandidates {
		hits = hits[:pickCandidates]
	}
	return hits, nil
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
