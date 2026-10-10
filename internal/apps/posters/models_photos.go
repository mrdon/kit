package posters

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/posterrender"
)

// PhotoStatus is where a photo is in its life: listed from Drive but not
// yet described, described and offered, or gone from Drive.
type PhotoStatus string

// Photo statuses.
const (
	PhotoPending PhotoStatus = "pending"
	PhotoIndexed PhotoStatus = "indexed"
	PhotoRemoved PhotoStatus = "removed"
)

// Photo is one row of the index. No bytes: Drive holds the file.
type Photo struct {
	ID              uuid.UUID   `json:"id"`
	TenantID        uuid.UUID   `json:"tenant_id"`
	DriveFileID     string      `json:"drive_file_id"`
	DriveModifiedAt string      `json:"drive_modified_at"`
	Folder          string      `json:"folder"`
	Filename        string      `json:"filename"`
	Width           int         `json:"width"`
	Height          int         `json:"height"`
	Orientation     string      `json:"orientation"`
	Status          PhotoStatus `json:"status"`
	Description     string      `json:"description"`
	Tags            []string    `json:"tags"`
	FocusX          float64     `json:"focus_x"`
	FocusY          float64     `json:"focus_y"`
	Notes           string      `json:"notes"`
	C2PA            string      `json:"c2pa"`
	IndexedBy       string      `json:"indexed_by"`
	IndexedAt       *time.Time  `json:"indexed_at,omitempty"`
	CreatedAt       time.Time   `json:"created_at"`
	UpdatedAt       time.Time   `json:"updated_at"`
}

// Ref is the photo as the renderer fetches it.
func (p Photo) Ref() posterrender.ImageRef {
	return posterrender.ImageRef{ID: p.ID.String(), Source: posterrender.SourceDrive, FileID: p.DriveFileID, Modified: p.DriveModifiedAt, Folder: p.Folder}
}

// Use is the photo cropped around its focus point.
func (p Photo) Use() posterrender.PhotoUse {
	return posterrender.PhotoUse{ID: p.ID.String(), FocusX: p.FocusX, FocusY: p.FocusY, Zoom: 1}
}

// Offerable reports whether the generator and search may use the photo.
func (p Photo) Offerable() bool { return p.Status == PhotoIndexed && p.C2PA != "ai" }

const photoColumns = `id, tenant_id, drive_file_id, drive_modified_at, folder, filename, width, height, orientation,
	status, description, tags, focus_x, focus_y, notes, c2pa, indexed_by, indexed_at, created_at, updated_at`

func scanPhoto(row pgx.Row) (*Photo, error) {
	var p Photo
	var fx, fy float32
	err := row.Scan(&p.ID, &p.TenantID, &p.DriveFileID, &p.DriveModifiedAt, &p.Folder, &p.Filename, &p.Width, &p.Height, &p.Orientation,
		&p.Status, &p.Description, &p.Tags, &fx, &fy, &p.Notes, &p.C2PA, &p.IndexedBy, &p.IndexedAt, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	p.FocusX, p.FocusY = float64(fx), float64(fy)
	if p.Tags == nil {
		p.Tags = []string{}
	}
	return &p, nil
}

func scanPhotos(rows pgx.Rows) ([]Photo, error) {
	defer rows.Close()
	out := []Photo{}
	for rows.Next() {
		p, err := scanPhoto(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// upsertListedPhoto records a file the sync found. A changed Drive
// modifiedTime resets the size facts (the renderer re-inspects); the
// description survives, since it is keyed by file and the subject rarely
// changes when a file is re-exported.
func upsertListedPhoto(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, fileID, modified, folder, filename string) (*Photo, bool, error) {
	var changed bool
	p, err := scanPhoto(pool.QueryRow(ctx, `
		INSERT INTO app_poster_photos (tenant_id, drive_file_id, drive_modified_at, folder, filename)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (tenant_id, drive_file_id) DO UPDATE SET
			drive_modified_at = EXCLUDED.drive_modified_at,
			folder = EXCLUDED.folder,
			filename = EXCLUDED.filename,
			status = CASE WHEN app_poster_photos.status = 'removed' THEN 'pending' ELSE app_poster_photos.status END,
			updated_at = now()
		RETURNING `+photoColumns, tenantID, fileID, modified, folder, filename))
	if err != nil {
		return nil, false, fmt.Errorf("recording listed photo: %w", err)
	}
	changed = p.Width == 0 || p.DriveModifiedAt != modified
	return p, changed, nil
}

func setPhotoFacts(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID, width, height int, orientation, c2pa string) error {
	_, err := pool.Exec(ctx, `
		UPDATE app_poster_photos SET width = $3, height = $4, orientation = $5, c2pa = $6, updated_at = now()
		WHERE tenant_id = $1 AND id = $2`, tenantID, id, width, height, orientation, c2pa)
	if err != nil {
		return fmt.Errorf("recording photo facts: %w", err)
	}
	return nil
}

// markRemovedExcept flags every photo the latest listing did not include.
func markRemovedExcept(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, seen []string) (int, error) {
	tag, err := pool.Exec(ctx, `
		UPDATE app_poster_photos SET status = 'removed', updated_at = now()
		WHERE tenant_id = $1 AND status <> 'removed' AND NOT (drive_file_id = ANY($2))`, tenantID, seen)
	if err != nil {
		return 0, fmt.Errorf("marking removed photos: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// IndexEntry is what an indexer writes back for one photo.
type IndexEntry struct {
	ID          uuid.UUID
	Description string
	Tags        []string
	FocusX      float64
	FocusY      float64
	Notes       string
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// indexPhoto writes a description and marks the photo indexed.
func indexPhoto(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, e IndexEntry, by string) (*Photo, error) {
	if e.Tags == nil {
		e.Tags = []string{}
	}
	probe := Photo{Description: e.Description, Tags: e.Tags, Notes: e.Notes}
	p, err := scanPhoto(pool.QueryRow(ctx, `
		UPDATE app_poster_photos SET
			description = $3, tags = $4, focus_x = $5, focus_y = $6, notes = $7,
			status = CASE WHEN status = 'removed' THEN status ELSE 'indexed' END,
			indexed_by = $8, indexed_at = now(), search_text = $9 || ' ' || replace(folder, '-', ' '), updated_at = now()
		WHERE tenant_id = $1 AND id = $2
		RETURNING `+photoColumns,
		tenantID, e.ID, strings.TrimSpace(e.Description), e.Tags, float32(clamp01(e.FocusX)), float32(clamp01(e.FocusY)),
		strings.TrimSpace(e.Notes), by, strings.Join([]string{probe.Description, strings.Join(probe.Tags, " "), probe.Notes}, " ")))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("indexing photo: %w", err)
	}
	return p, nil
}

// setPhotoHints saves focus, tags and notes without changing the photo's
// status, so a pending photo's focus point can be placed before anyone has
// described it.
func setPhotoHints(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, e IndexEntry) (*Photo, error) {
	if e.Tags == nil {
		e.Tags = []string{}
	}
	p, err := scanPhoto(pool.QueryRow(ctx, `
		UPDATE app_poster_photos SET tags = $3, focus_x = $4, focus_y = $5, notes = $6,
			search_text = description || ' ' || $7 || ' ' || replace(folder, '-', ' '), updated_at = now()
		WHERE tenant_id = $1 AND id = $2
		RETURNING `+photoColumns,
		tenantID, e.ID, e.Tags, float32(clamp01(e.FocusX)), float32(clamp01(e.FocusY)), strings.TrimSpace(e.Notes),
		strings.Join(e.Tags, " ")+" "+strings.TrimSpace(e.Notes)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("saving photo hints: %w", err)
	}
	return p, nil
}

func getPhoto(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID) (*Photo, error) {
	p, err := scanPhoto(pool.QueryRow(ctx, `SELECT `+photoColumns+` FROM app_poster_photos WHERE tenant_id = $1 AND id = $2`, tenantID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("loading photo: %w", err)
	}
	return p, nil
}

func getPhotos(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, ids []uuid.UUID) ([]Photo, error) {
	rows, err := pool.Query(ctx, `SELECT `+photoColumns+` FROM app_poster_photos WHERE tenant_id = $1 AND id = ANY($2)`, tenantID, ids)
	if err != nil {
		return nil, fmt.Errorf("loading photos: %w", err)
	}
	return scanPhotos(rows)
}

// PhotoFilter narrows a listing.
type PhotoFilter struct {
	Status PhotoStatus
	Folder string
	Limit  int
}

func listPhotos(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, f PhotoFilter) ([]Photo, error) {
	limit := f.Limit
	if limit <= 0 || limit > 2000 {
		limit = 2000
	}
	rows, err := pool.Query(ctx, `
		SELECT `+photoColumns+` FROM app_poster_photos
		WHERE tenant_id = $1 AND ($2 = '' OR status = $2) AND ($3 = '' OR folder = $3)
		ORDER BY folder, filename LIMIT $4`, tenantID, string(f.Status), f.Folder, limit)
	if err != nil {
		return nil, fmt.Errorf("listing photos: %w", err)
	}
	return scanPhotos(rows)
}

// searchPhotos is the full-text search over indexed, offerable photos.
// Plain-language queries go through websearch_to_tsquery first (every word
// must match), then fall back to any-word matching ranked by overlap, so
// an event title pasted in whole still finds the photo that shares two of
// its five words.
func searchPhotos(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, query string, limit int) ([]Photo, error) {
	if limit <= 0 {
		limit = 10
	}
	if strings.TrimSpace(query) == "" {
		return []Photo{}, nil
	}
	hits, err := searchPhotosWith(ctx, pool, tenantID, "websearch_to_tsquery('english', $2)", query, limit)
	if err != nil || len(hits) > 0 {
		return hits, err
	}
	any := anyWordQuery(query)
	if any == "" {
		return []Photo{}, nil
	}
	return searchPhotosWith(ctx, pool, tenantID, "to_tsquery('english', $2)", any, limit)
}

func searchPhotosWith(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, tsquery, query string, limit int) ([]Photo, error) {
	rows, err := pool.Query(ctx, `
		SELECT `+photoColumns+` FROM app_poster_photos
		WHERE tenant_id = $1 AND status = 'indexed' AND c2pa <> 'ai'
		  AND to_tsvector('english', search_text) @@ `+tsquery+`
		ORDER BY ts_rank(to_tsvector('english', search_text), `+tsquery+`) DESC, filename
		LIMIT $3`, tenantID, query, limit)
	if err != nil {
		return nil, fmt.Errorf("searching photos: %w", err)
	}
	return scanPhotos(rows)
}

var wordChars = regexp.MustCompile(`[^\p{L}\p{N}]+`)

// anyWordQuery turns free text into an OR tsquery of its words.
func anyWordQuery(query string) string {
	var terms []string
	for w := range strings.FieldsSeq(wordChars.ReplaceAllString(strings.ToLower(query), " ")) {
		if len(w) >= 2 {
			terms = append(terms, w)
		}
	}
	return strings.Join(terms, " | ")
}

// photoCounts is the admin page's "12 waiting for descriptions".
func photoCounts(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (map[PhotoStatus]int, error) {
	rows, err := pool.Query(ctx, `SELECT status, count(*) FROM app_poster_photos WHERE tenant_id = $1 GROUP BY status`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("counting photos: %w", err)
	}
	defer rows.Close()
	out := map[PhotoStatus]int{}
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			return nil, err
		}
		out[PhotoStatus(s)] = n
	}
	return out, rows.Err()
}
