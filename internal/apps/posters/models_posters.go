package posters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/posterrender"
)

// ErrNotFound is the app's not-found error.
var ErrNotFound = errors.New("not found")

// Poster is one event's poster: a chain of versions, one of which may be
// on the event.
type Poster struct {
	ID               uuid.UUID       `json:"id"`
	TenantID         uuid.UUID       `json:"tenant_id"`
	EventID          *uuid.UUID      `json:"event_id,omitempty"`
	Title            string          `json:"title"`
	CurrentVersionID *uuid.UUID      `json:"current_version_id,omitempty"`
	SetVersionID     *uuid.UUID      `json:"set_version_id,omitempty"`
	Facts            json.RawMessage `json:"facts,omitempty"`
	FactsHash        string          `json:"-"`
	Stale            bool            `json:"stale"`
	CreatedBy        *uuid.UUID      `json:"created_by,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

// Version is one state of a poster's source. Options share a batch.
type Version struct {
	ID                uuid.UUID               `json:"id"`
	PosterID          uuid.UUID               `json:"poster_id"`
	ParentID          *uuid.UUID              `json:"parent_id,omitempty"`
	BatchID           *uuid.UUID              `json:"batch_id,omitempty"`
	TemplateID        *uuid.UUID              `json:"template_id,omitempty"`
	TemplateVersionID *uuid.UUID              `json:"template_version_id,omitempty"`
	Source            string                  `json:"source"`
	Content           posterrender.Content    `json:"content"`
	Photos            []posterrender.PhotoUse `json:"photos"`
	Ground            string                  `json:"ground"`
	Format            string                  `json:"format"`
	Problems          []string                `json:"problems"`
	Instruction       string                  `json:"instruction"`
	Author            string                  `json:"author"`
	Picked            bool                    `json:"picked"`
	CreatedAt         time.Time               `json:"created_at"`
}

const posterColumns = `id, tenant_id, event_id, title, current_version_id, set_version_id, facts, facts_hash, stale, created_by, created_at, updated_at`

func scanPoster(row pgx.Row) (*Poster, error) {
	var p Poster
	var facts []byte
	if err := row.Scan(&p.ID, &p.TenantID, &p.EventID, &p.Title, &p.CurrentVersionID, &p.SetVersionID, &facts, &p.FactsHash, &p.Stale, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	if len(facts) > 0 {
		p.Facts = json.RawMessage(facts)
	}
	return &p, nil
}

func getPoster(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID) (*Poster, error) {
	p, err := scanPoster(pool.QueryRow(ctx, `SELECT `+posterColumns+` FROM app_posters WHERE tenant_id = $1 AND id = $2`, tenantID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("loading poster: %w", err)
	}
	return p, nil
}

// posterForEvent finds the event's poster, or nil. One poster per event.
//
//nolint:nilnil // "no poster yet" is the common case, not a failure
func posterForEvent(ctx context.Context, pool *pgxpool.Pool, tenantID, eventID uuid.UUID) (*Poster, error) {
	p, err := scanPoster(pool.QueryRow(ctx, `SELECT `+posterColumns+` FROM app_posters WHERE tenant_id = $1 AND event_id = $2 ORDER BY created_at LIMIT 1`, tenantID, eventID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("loading event poster: %w", err)
	}
	return p, nil
}

func createPoster(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, eventID *uuid.UUID, title string, by *uuid.UUID) (*Poster, error) {
	p, err := scanPoster(pool.QueryRow(ctx, `
		INSERT INTO app_posters (tenant_id, event_id, title, created_by) VALUES ($1, $2, $3, $4) RETURNING `+posterColumns,
		tenantID, eventID, title, by))
	if err != nil {
		return nil, fmt.Errorf("creating poster: %w", err)
	}
	return p, nil
}

func listPosters(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) ([]Poster, error) {
	rows, err := pool.Query(ctx, `SELECT `+posterColumns+` FROM app_posters WHERE tenant_id = $1 ORDER BY updated_at DESC LIMIT 500`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("listing posters: %w", err)
	}
	defer rows.Close()
	out := []Poster{}
	for rows.Next() {
		p, err := scanPoster(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func setCurrentVersion(ctx context.Context, pool *pgxpool.Pool, tenantID, posterID, versionID uuid.UUID) error {
	_, err := pool.Exec(ctx, `UPDATE app_posters SET current_version_id = $3, updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenantID, posterID, versionID)
	if err != nil {
		return fmt.Errorf("setting current version: %w", err)
	}
	return nil
}

// setOnEvent records which version is on the event and the facts it used.
func setOnEvent(ctx context.Context, pool *pgxpool.Pool, tenantID, posterID, versionID uuid.UUID, facts json.RawMessage, hash string) error {
	_, err := pool.Exec(ctx, `
		UPDATE app_posters SET set_version_id = $3, current_version_id = $3, facts = $4, facts_hash = $5, stale = false, updated_at = now()
		WHERE tenant_id = $1 AND id = $2`, tenantID, posterID, versionID, []byte(facts), hash)
	if err != nil {
		return fmt.Errorf("recording poster on event: %w", err)
	}
	return nil
}

func setStale(ctx context.Context, pool *pgxpool.Pool, tenantID, posterID uuid.UUID, stale bool) error {
	_, err := pool.Exec(ctx, `UPDATE app_posters SET stale = $3, updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenantID, posterID, stale)
	if err != nil {
		return fmt.Errorf("flagging poster: %w", err)
	}
	return nil
}

func deletePoster(ctx context.Context, pool *pgxpool.Pool, tenantID, posterID uuid.UUID) error {
	tag, err := pool.Exec(ctx, `DELETE FROM app_posters WHERE tenant_id = $1 AND id = $2`, tenantID, posterID)
	if err != nil {
		return fmt.Errorf("deleting poster: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

const versionColumns = `id, poster_id, parent_id, batch_id, template_id, template_version_id, source, content, photos, ground, format, problems, instruction, author, picked, created_at`

func scanVersion(row pgx.Row) (*Version, error) {
	var v Version
	var content, photos, problems []byte
	if err := row.Scan(&v.ID, &v.PosterID, &v.ParentID, &v.BatchID, &v.TemplateID, &v.TemplateVersionID, &v.Source, &content, &photos, &v.Ground, &v.Format, &problems, &v.Instruction, &v.Author, &v.Picked, &v.CreatedAt); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(content, &v.Content)
	_ = json.Unmarshal(photos, &v.Photos)
	_ = json.Unmarshal(problems, &v.Problems)
	if v.Photos == nil {
		v.Photos = []posterrender.PhotoUse{}
	}
	if v.Problems == nil {
		v.Problems = []string{}
	}
	if v.Content.Details == nil {
		v.Content.Details = []posterrender.Detail{}
	}
	return &v, nil
}

// VersionInput is a version to save.
type VersionInput struct {
	PosterID          uuid.UUID
	ParentID          *uuid.UUID
	BatchID           *uuid.UUID
	TemplateID        *uuid.UUID
	TemplateVersionID *uuid.UUID
	Source            string
	Content           posterrender.Content
	Photos            []posterrender.PhotoUse
	Ground            string
	Format            string
	Problems          []string
	Instruction       string
	Author            string
	Picked            bool
}

func insertVersion(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, in VersionInput) (*Version, error) {
	if in.Photos == nil {
		in.Photos = []posterrender.PhotoUse{}
	}
	if in.Problems == nil {
		in.Problems = []string{}
	}
	content, _ := json.Marshal(in.Content)
	photos, _ := json.Marshal(in.Photos)
	problems, _ := json.Marshal(in.Problems)
	v, err := scanVersion(pool.QueryRow(ctx, `
		INSERT INTO app_poster_versions (tenant_id, poster_id, parent_id, batch_id, template_id, template_version_id, source, content, photos, ground, format, problems, instruction, author, picked)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		RETURNING `+versionColumns,
		tenantID, in.PosterID, in.ParentID, in.BatchID, in.TemplateID, in.TemplateVersionID, in.Source, content, photos, in.Ground, in.Format, problems, in.Instruction, in.Author, in.Picked))
	if err != nil {
		return nil, fmt.Errorf("saving poster version: %w", err)
	}
	return v, nil
}

func getVersion(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID) (*Version, error) {
	v, err := scanVersion(pool.QueryRow(ctx, `SELECT `+versionColumns+` FROM app_poster_versions WHERE tenant_id = $1 AND id = $2`, tenantID, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("loading poster version: %w", err)
	}
	return v, nil
}

// listVersions returns a poster's history, newest first. Unpicked options
// are left out unless withOptions is set: they are candidates, not history.
func listVersions(ctx context.Context, pool *pgxpool.Pool, tenantID, posterID uuid.UUID, withOptions bool) ([]Version, error) {
	rows, err := pool.Query(ctx, `
		SELECT `+versionColumns+` FROM app_poster_versions
		WHERE tenant_id = $1 AND poster_id = $2 AND ($3 OR batch_id IS NULL OR picked)
		ORDER BY created_at DESC LIMIT 200`, tenantID, posterID, withOptions)
	if err != nil {
		return nil, fmt.Errorf("listing poster versions: %w", err)
	}
	return scanVersions(rows)
}

func listBatch(ctx context.Context, pool *pgxpool.Pool, tenantID, batchID uuid.UUID) ([]Version, error) {
	rows, err := pool.Query(ctx, `SELECT `+versionColumns+` FROM app_poster_versions WHERE tenant_id = $1 AND batch_id = $2 ORDER BY created_at`, tenantID, batchID)
	if err != nil {
		return nil, fmt.Errorf("listing option batch: %w", err)
	}
	return scanVersions(rows)
}

func scanVersions(rows pgx.Rows) ([]Version, error) {
	defer rows.Close()
	out := []Version{}
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

func markPicked(ctx context.Context, pool *pgxpool.Pool, tenantID, versionID uuid.UUID) error {
	_, err := pool.Exec(ctx, `UPDATE app_poster_versions SET picked = true WHERE tenant_id = $1 AND id = $2`, tenantID, versionID)
	if err != nil {
		return fmt.Errorf("marking version picked: %w", err)
	}
	return nil
}

// shownTemplateIDs lists the templates a poster's options have already
// used, so "more options" can avoid them.
func shownTemplateIDs(ctx context.Context, pool *pgxpool.Pool, tenantID, posterID uuid.UUID) ([]string, error) {
	rows, err := pool.Query(ctx, `
		SELECT DISTINCT template_id::text FROM app_poster_versions
		WHERE tenant_id = $1 AND poster_id = $2 AND batch_id IS NOT NULL AND template_id IS NOT NULL`, tenantID, posterID)
	if err != nil {
		return nil, fmt.Errorf("listing shown templates: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// pruneOptions drops unpicked option versions older than the retention
// window. Picked ones and their edit history stay.
func pruneOptions(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, olderThan time.Duration) (int, error) {
	tag, err := pool.Exec(ctx, `
		DELETE FROM app_poster_versions
		WHERE tenant_id = $1 AND batch_id IS NOT NULL AND NOT picked AND created_at < now() - $2::interval
		  AND id NOT IN (SELECT current_version_id FROM app_posters WHERE tenant_id = $1 AND current_version_id IS NOT NULL)`,
		tenantID, olderThan.String())
	if err != nil {
		return 0, fmt.Errorf("pruning options: %w", err)
	}
	return int(tag.RowsAffected()), nil
}
