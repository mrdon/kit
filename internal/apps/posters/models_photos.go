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
	ID              uuid.UUID         `json:"id"`
	TenantID        uuid.UUID         `json:"tenant_id"`
	DriveFileID     string            `json:"drive_file_id"`
	DriveModifiedAt string            `json:"drive_modified_at"`
	Folder          string            `json:"folder"`
	Filename        string            `json:"filename"`
	Width           int               `json:"width"`
	Height          int               `json:"height"`
	Orientation     string            `json:"orientation"`
	Status          PhotoStatus       `json:"status"`
	Description     string            `json:"description"`
	Tags            []string          `json:"tags"`
	FocusX          float64           `json:"focus_x"`
	FocusY          float64           `json:"focus_y"`
	Notes           string            `json:"notes"`
	C2PA            posterrender.C2PA `json:"c2pa"`
	IndexedBy       string            `json:"indexed_by"`
	IndexedAt       *time.Time        `json:"indexed_at,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

// Ref is the photo as the renderer fetches it for a render: the original.
func (p Photo) Ref() posterrender.ImageRef {
	return posterrender.ImageRef{ID: p.ID.String(), Source: posterrender.SourceDrive, FileID: p.DriveFileID, Modified: p.DriveModifiedAt, Folder: p.Folder}
}

// LookRef is the photo for looking at, not rendering: Drive's sized
// thumbnail first, the original as fallback. A contact sheet of twelve
// costs twelve small fetches instead of twelve originals, which is what
// makes describing a library of thousands affordable.
func (p Photo) LookRef(size int) posterrender.ImageRef {
	r := p.Ref()
	r.ThumbURL = driveThumbnailURL(p.DriveFileID, size)
	r.Modified = fmt.Sprintf("%s#look%d", p.DriveModifiedAt, size)
	return r
}

// Use is the photo cropped around its focus point.
func (p Photo) Use() posterrender.PhotoUse {
	return posterrender.PhotoUse{ID: p.ID.String(), FocusX: p.FocusX, FocusY: p.FocusY, Zoom: 1}
}

// Offerable reports whether the generator and search may use the photo.
func (p Photo) Offerable() bool { return p.Status == PhotoIndexed && p.C2PA != posterrender.C2PAAI }

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

// recordListedPhotos records every file one listing found, in one
// statement per 500 files. A new file starts pending and unmeasured; a
// file whose Drive modified time moved has its measurements reset so the
// measure task looks again; dimensions the listing already knows (the
// Drive API reports them) are stored at once, with provenance left to the
// renderer when it first fetches the original. It returns how many were new.
func recordListedPhotos(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, files []DriveFile) (int, error) {
	inserted := 0
	for start := 0; start < len(files); start += 500 {
		chunk := files[start:min(start+500, len(files))]
		n, err := recordChunk(ctx, pool, tenantID, chunk)
		if err != nil {
			return 0, err
		}
		inserted += n
	}
	return inserted, nil
}

func recordChunk(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, chunk []DriveFile) (int, error) {
	n := len(chunk)
	ids, mods, folders, names, orients := make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n)
	widths, heights := make([]int32, n), make([]int32, n)
	for i, f := range chunk {
		ids[i], mods[i], folders[i], names[i] = f.ID, f.Modified, f.Folder, f.Name
		widths[i], heights[i], orients[i] = int32(f.Width), int32(f.Height), orientationOf(f.Width, f.Height) //nolint:gosec // image dimensions
	}
	rows, err := pool.Query(ctx, `
		INSERT INTO app_poster_photos (tenant_id, drive_file_id, drive_modified_at, folder, filename, width, height, orientation, c2pa)
		SELECT $1, u.file_id, u.modified, u.folder, u.filename, u.width, u.height, u.orientation,
		       CASE WHEN u.width > 0 THEN $2 ELSE 'none' END
		FROM unnest($3::text[], $4::text[], $5::text[], $6::text[], $7::int[], $8::int[], $9::text[])
		  AS u(file_id, modified, folder, filename, width, height, orientation)
		ON CONFLICT (tenant_id, drive_file_id) DO UPDATE SET
			drive_modified_at = EXCLUDED.drive_modified_at,
			folder = EXCLUDED.folder,
			filename = EXCLUDED.filename,
			width = CASE WHEN EXCLUDED.width > 0 THEN EXCLUDED.width
			             WHEN app_poster_photos.drive_modified_at <> EXCLUDED.drive_modified_at THEN 0
			             ELSE app_poster_photos.width END,
			height = CASE WHEN EXCLUDED.width > 0 THEN EXCLUDED.height
			              WHEN app_poster_photos.drive_modified_at <> EXCLUDED.drive_modified_at THEN 0
			              ELSE app_poster_photos.height END,
			orientation = CASE WHEN EXCLUDED.width > 0 THEN EXCLUDED.orientation
			                   WHEN app_poster_photos.drive_modified_at <> EXCLUDED.drive_modified_at THEN ''
			                   ELSE app_poster_photos.orientation END,
			c2pa = CASE WHEN EXCLUDED.width > 0 AND app_poster_photos.drive_modified_at <> EXCLUDED.drive_modified_at THEN EXCLUDED.c2pa
			            ELSE app_poster_photos.c2pa END,
			status = CASE WHEN app_poster_photos.status = 'removed' THEN 'pending' ELSE app_poster_photos.status END,
			updated_at = now()
		RETURNING (xmax = 0)`,
		tenantID, string(posterrender.C2PAUnchecked), ids, mods, folders, names, widths, heights, orients)
	if err != nil {
		return 0, fmt.Errorf("recording listed photos: %w", err)
	}
	defer rows.Close()
	inserted := 0
	for rows.Next() {
		var isNew bool
		if err := rows.Scan(&isNew); err != nil {
			return 0, fmt.Errorf("recording listed photos: %w", err)
		}
		if isNew {
			inserted++
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("recording listed photos: %w", err)
	}
	return inserted, nil
}

// orientationOf uses the renderer's thresholds so the two agree.
func orientationOf(w, h int) string {
	switch {
	case w <= 0 || h <= 0:
		return ""
	case float64(w) > float64(h)*1.1:
		return "landscape"
	case float64(h) > float64(w)*1.1:
		return "portrait"
	default:
		return "square"
	}
}

// upsertListedPhoto records one file and says whether it needs measuring
// again: new, changed on Drive, or never measured. Tests use it; the sync
// goes through recordListedPhotos.
func upsertListedPhoto(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, fileID, modified, folder, filename string) (*Photo, bool, error) {
	before, err := getPhotoByFileID(ctx, pool, tenantID, fileID)
	if err != nil {
		return nil, false, err
	}
	if _, err := recordListedPhotos(ctx, pool, tenantID, []DriveFile{{ID: fileID, Name: filename, Folder: folder, Modified: modified}}); err != nil {
		return nil, false, err
	}
	p, err := getPhotoByFileID(ctx, pool, tenantID, fileID)
	if err != nil {
		return nil, false, err
	}
	changed := before == nil || before.DriveModifiedAt != modified || p.Width == 0
	return p, changed, nil
}

func getPhotoByFileID(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, fileID string) (*Photo, error) {
	p, err := scanPhoto(pool.QueryRow(ctx, `SELECT `+photoColumns+` FROM app_poster_photos WHERE tenant_id = $1 AND drive_file_id = $2`, tenantID, fileID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // not found is not an error here
	}
	if err != nil {
		return nil, fmt.Errorf("loading photo by file id: %w", err)
	}
	return p, nil
}

// listUnmeasured returns photos still without dimensions, oldest attempt
// first, so one that keeps failing rotates to the back.
func listUnmeasured(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, limit int) ([]Photo, error) {
	rows, err := pool.Query(ctx, `
		SELECT `+photoColumns+` FROM app_poster_photos
		WHERE tenant_id = $1 AND width = 0 AND status <> $2
		ORDER BY updated_at, filename LIMIT $3`, tenantID, PhotoRemoved, limit)
	if err != nil {
		return nil, fmt.Errorf("listing unmeasured photos: %w", err)
	}
	return scanPhotos(rows)
}

func countUnmeasured(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (int, error) {
	var n int
	err := pool.QueryRow(ctx, `SELECT count(*) FROM app_poster_photos WHERE tenant_id = $1 AND width = 0 AND status <> $2`, tenantID, PhotoRemoved).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting unmeasured photos: %w", err)
	}
	return n, nil
}

// touchPhoto moves a photo to the back of the measuring queue.
func touchPhoto(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID) error {
	_, err := pool.Exec(ctx, `UPDATE app_poster_photos SET updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenantID, id)
	if err != nil {
		return fmt.Errorf("touching photo: %w", err)
	}
	return nil
}

func setPhotoFacts(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID, width, height int, orientation string, c2pa posterrender.C2PA) error {
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
	Offset int
}

const photoPageMax = 2000

func listPhotos(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, f PhotoFilter) ([]Photo, error) {
	limit := f.Limit
	if limit <= 0 || limit > photoPageMax {
		limit = photoPageMax
	}
	rows, err := pool.Query(ctx, `
		SELECT `+photoColumns+` FROM app_poster_photos
		WHERE tenant_id = $1 AND ($2 = '' OR status = $2) AND ($3 = '' OR folder = $3)
		ORDER BY folder, filename LIMIT $4 OFFSET $5`, tenantID, string(f.Status), f.Folder, limit, max(f.Offset, 0))
	if err != nil {
		return nil, fmt.Errorf("listing photos: %w", err)
	}
	return scanPhotos(rows)
}

// countPhotos is the total behind a filtered page.
func countPhotos(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, f PhotoFilter) (int, error) {
	var n int
	err := pool.QueryRow(ctx, `
		SELECT count(*) FROM app_poster_photos
		WHERE tenant_id = $1 AND ($2 = '' OR status = $2) AND ($3 = '' OR folder = $3)`, tenantID, string(f.Status), f.Folder).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting photos: %w", err)
	}
	return n, nil
}

// listFolders is every set name in the library, without loading the rows.
func listFolders(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) ([]string, error) {
	rows, err := pool.Query(ctx, `SELECT DISTINCT folder FROM app_poster_photos WHERE tenant_id = $1 ORDER BY folder`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("listing photo folders: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
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
		WHERE tenant_id = $1 AND status = $4 AND c2pa <> $5
		  AND to_tsvector('english', search_text) @@ `+tsquery+`
		ORDER BY ts_rank(to_tsvector('english', search_text), `+tsquery+`) DESC, filename
		LIMIT $3`, tenantID, query, limit, PhotoIndexed, posterrender.C2PAAI)
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
