package posters

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/posterrender"
)

// BeerPhoto is a library photo of one beer, as another app needs it: enough
// to offer it and to crop it, not the whole index row.
type BeerPhoto struct {
	ID          uuid.UUID
	Description string
	Orientation string
	FocusX      float64
	FocusY      float64
}

// BeerPhotos lists the offerable library photos tagged with a beer, portraits
// first. The tag is the beer's name as the indexer writes it -- lowercase,
// words joined by hyphens, "wicked-nebula" -- so the lookup is a plain match
// on what indexing already recorded, with no model in the loop. A photo of
// three beers carries all three tags and is offered for each.
func (a *App) BeerPhotos(ctx context.Context, tenantID uuid.UUID, tag string, limit int) ([]BeerPhoto, error) {
	// An app that was registered but never initialised -- in a test, or a
	// process that does not run posters -- has no library to search.
	if tag == "" || a.pool == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 12
	}
	rows, err := a.pool.Query(ctx, `
		SELECT id, description, orientation, focus_x, focus_y FROM app_poster_photos
		WHERE tenant_id = $1 AND status = $2 AND c2pa <> $3 AND $4 = ANY(tags)
		ORDER BY (orientation = 'portrait') DESC, filename
		LIMIT $5`, tenantID, PhotoIndexed, posterrender.C2PAAI, tag, limit)
	if err != nil {
		return nil, fmt.Errorf("listing photos tagged %q: %w", tag, err)
	}
	defer rows.Close()
	var out []BeerPhoto
	for rows.Next() {
		var p BeerPhoto
		if err := rows.Scan(&p.ID, &p.Description, &p.Orientation, &p.FocusX, &p.FocusY); err != nil {
			return nil, fmt.Errorf("scanning photo: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
