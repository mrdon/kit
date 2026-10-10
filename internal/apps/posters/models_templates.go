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

// TemplateStatus is a template's lifecycle stage. Only active templates
// feed the option generator.
type TemplateStatus string

// Template statuses.
const (
	TemplateDraft    TemplateStatus = "draft"
	TemplateActive   TemplateStatus = "active"
	TemplateArchived TemplateStatus = "archived"
)

// TemplateOrigin says where a template came from. Mirrored by the
// console's PosterTemplateOrigin.
type TemplateOrigin string

const (
	OriginBuiltin TemplateOrigin = "builtin"
	OriginPoster  TemplateOrigin = "poster" // saved from a finished poster
	OriginImage   TemplateOrigin = "image"  // rebuilt from a reference image
	OriginChat    TemplateOrigin = "chat"   // written in template chat
)

// BuiltinVisibility is the status word a built-in takes instead of the
// tenant lifecycle: hidden from this workspace's generator, or offered.
type BuiltinVisibility string

const (
	BuiltinHidden  BuiltinVisibility = "hidden"
	BuiltinVisible BuiltinVisibility = "visible"
)

// Template is a layout that takes content and draws a poster. Built-ins
// have no tenant and cannot be edited in place.
type Template struct {
	ID                    uuid.UUID                 `json:"id"`
	TenantID              *uuid.UUID                `json:"tenant_id,omitempty"`
	BuiltinKey            string                    `json:"builtin_key,omitempty"`
	Name                  string                    `json:"name"`
	Description           string                    `json:"description"`
	Status                TemplateStatus            `json:"status"`
	Origin                TemplateOrigin            `json:"origin"`
	Meta                  posterrender.TemplateMeta `json:"meta"`
	CurrentVersionID      *uuid.UUID                `json:"current_version_id,omitempty"`
	ParentTemplateID      *uuid.UUID                `json:"parent_template_id,omitempty"`
	SourcePosterID        *uuid.UUID                `json:"source_poster_id,omitempty"`
	ReferenceAttachmentID *uuid.UUID                `json:"reference_attachment_id,omitempty"`
	CreatedBy             *uuid.UUID                `json:"created_by,omitempty"`
	CreatedAt             time.Time                 `json:"created_at"`
	UpdatedAt             time.Time                 `json:"updated_at"`
	// Hidden is set for a built-in this tenant chose not to offer.
	Hidden bool `json:"hidden"`
	// Pick statistics, filled by listTemplates.
	Picks    int        `json:"picks"`
	LastUsed *time.Time `json:"last_used,omitempty"`
}

// Builtin reports whether the template ships with Kit.
func (t Template) Builtin() bool { return t.TenantID == nil }

// TemplateVersion is one saved state of a template's source.
type TemplateVersion struct {
	ID         uuid.UUID  `json:"id"`
	TemplateID uuid.UUID  `json:"template_id"`
	ParentID   *uuid.UUID `json:"parent_id,omitempty"`
	Source     string     `json:"source"`
	Summary    string     `json:"summary"`
	Author     Author     `json:"author"`
	CreatedBy  *uuid.UUID `json:"created_by,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

const templateColumns = `t.id, t.tenant_id, t.builtin_key, t.name, t.description, t.status, t.origin, t.meta,
	t.current_version_id, t.parent_template_id, t.source_poster_id, t.reference_attachment_id, t.created_by, t.created_at, t.updated_at`

func scanTemplate(row pgx.Row, withStats bool) (*Template, error) {
	var t Template
	var key *string
	var meta []byte
	dest := []any{&t.ID, &t.TenantID, &key, &t.Name, &t.Description, &t.Status, &t.Origin, &meta,
		&t.CurrentVersionID, &t.ParentTemplateID, &t.SourcePosterID, &t.ReferenceAttachmentID, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt}
	if withStats {
		dest = append(dest, &t.Hidden, &t.Picks, &t.LastUsed)
	}
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	if key != nil {
		t.BuiltinKey = *key
	}
	if len(meta) > 0 {
		_ = json.Unmarshal(meta, &t.Meta)
	}
	return &t, nil
}

// listTemplates returns the tenant's own templates plus every built-in,
// with pick counts from the tenant's own picked versions.
func listTemplates(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, status TemplateStatus) ([]Template, error) {
	rows, err := pool.Query(ctx, `
		SELECT `+templateColumns+`,
		       EXISTS (SELECT 1 FROM app_poster_template_hidden h WHERE h.tenant_id = $1 AND h.template_id = t.id) AS hidden,
		       (SELECT count(*) FROM app_poster_versions v WHERE v.tenant_id = $1 AND v.template_id = t.id AND v.picked) AS picks,
		       (SELECT max(v.created_at) FROM app_poster_versions v WHERE v.tenant_id = $1 AND v.template_id = t.id AND v.picked) AS last_used
		FROM app_poster_templates t
		WHERE (t.tenant_id = $1 OR t.tenant_id IS NULL) AND ($2 = '' OR t.status = $2)
		ORDER BY t.tenant_id IS NULL, t.name`, tenantID, string(status))
	if err != nil {
		return nil, fmt.Errorf("listing templates: %w", err)
	}
	defer rows.Close()
	out := []Template{}
	for rows.Next() {
		t, err := scanTemplate(rows, true)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func getTemplate(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID) (*Template, error) {
	t, err := scanTemplate(pool.QueryRow(ctx, `
		SELECT `+templateColumns+`,
		       EXISTS (SELECT 1 FROM app_poster_template_hidden h WHERE h.tenant_id = $1 AND h.template_id = t.id),
		       (SELECT count(*) FROM app_poster_versions v WHERE v.tenant_id = $1 AND v.template_id = t.id AND v.picked),
		       (SELECT max(v.created_at) FROM app_poster_versions v WHERE v.tenant_id = $1 AND v.template_id = t.id AND v.picked)
		FROM app_poster_templates t
		WHERE (t.tenant_id = $1 OR t.tenant_id IS NULL) AND t.id = $2`, tenantID, id), true)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("loading template: %w", err)
	}
	return t, nil
}

// TemplateInput is what a new template needs.
type TemplateInput struct {
	Name                  string
	Description           string
	Origin                TemplateOrigin
	Meta                  posterrender.TemplateMeta
	Source                string
	Summary               string
	Author                Author
	ParentTemplateID      *uuid.UUID
	SourcePosterID        *uuid.UUID
	ReferenceAttachmentID *uuid.UUID
	CreatedBy             *uuid.UUID
}

// createTemplate inserts a draft template and its first version in one
// transaction.
func createTemplate(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, in TemplateInput) (*Template, error) {
	meta, _ := json.Marshal(in.Meta)
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating template: %w", err)
	}
	defer tx.Rollback(ctx)
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO app_poster_templates (tenant_id, name, description, status, origin, meta, parent_template_id, source_poster_id, reference_attachment_id, created_by)
		VALUES ($1, $2, $3, 'draft', $4, $5, $6, $7, $8, $9) RETURNING id`,
		tenantID, in.Name, in.Description, in.Origin, meta, in.ParentTemplateID, in.SourcePosterID, in.ReferenceAttachmentID, in.CreatedBy).Scan(&id); err != nil {
		return nil, fmt.Errorf("creating template: %w", err)
	}
	var vid uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO app_poster_template_versions (tenant_id, template_id, source, summary, author, created_by)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`, tenantID, id, in.Source, in.Summary, in.Author, in.CreatedBy).Scan(&vid); err != nil {
		return nil, fmt.Errorf("creating template version: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE app_poster_templates SET current_version_id = $3 WHERE tenant_id = $1 AND id = $2`, tenantID, id, vid); err != nil {
		return nil, fmt.Errorf("pointing template at its version: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("creating template: %w", err)
	}
	return getTemplate(ctx, pool, tenantID, id)
}

// addTemplateVersion saves a new version of a tenant template and makes it
// current. Built-ins are refused: they have no tenant to own the edit.
func addTemplateVersion(ctx context.Context, pool *pgxpool.Pool, tenantID, templateID uuid.UUID, source, summary string, author Author, meta posterrender.TemplateMeta, by *uuid.UUID) (*TemplateVersion, error) {
	metaJSON, _ := json.Marshal(meta)
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("saving template version: %w", err)
	}
	defer tx.Rollback(ctx)
	var v TemplateVersion
	err = tx.QueryRow(ctx, `
		INSERT INTO app_poster_template_versions (tenant_id, template_id, parent_id, source, summary, author, created_by)
		SELECT $1, t.id, t.current_version_id, $3, $4, $5, $6 FROM app_poster_templates t WHERE t.tenant_id = $1 AND t.id = $2
		RETURNING id, template_id, parent_id, source, summary, author, created_by, created_at`,
		tenantID, templateID, source, summary, author, by).
		Scan(&v.ID, &v.TemplateID, &v.ParentID, &v.Source, &v.Summary, &v.Author, &v.CreatedBy, &v.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("saving template version: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE app_poster_templates SET current_version_id = $3, meta = $4, name = COALESCE(NULLIF($5, ''), name), description = COALESCE(NULLIF($6, ''), description), updated_at = now()
		WHERE tenant_id = $1 AND id = $2`, tenantID, templateID, v.ID, metaJSON, meta.Name, meta.Description); err != nil {
		return nil, fmt.Errorf("pointing template at its version: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("saving template version: %w", err)
	}
	return &v, nil
}

func listTemplateVersions(ctx context.Context, pool *pgxpool.Pool, tenantID, templateID uuid.UUID) ([]TemplateVersion, error) {
	rows, err := pool.Query(ctx, `
		SELECT v.id, v.template_id, v.parent_id, v.source, v.summary, v.author, v.created_by, v.created_at
		FROM app_poster_template_versions v JOIN app_poster_templates t ON t.id = v.template_id
		WHERE (t.tenant_id = $1 OR t.tenant_id IS NULL) AND v.template_id = $2
		ORDER BY v.created_at DESC`, tenantID, templateID)
	if err != nil {
		return nil, fmt.Errorf("listing template versions: %w", err)
	}
	defer rows.Close()
	out := []TemplateVersion{}
	for rows.Next() {
		var v TemplateVersion
		if err := rows.Scan(&v.ID, &v.TemplateID, &v.ParentID, &v.Source, &v.Summary, &v.Author, &v.CreatedBy, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func getTemplateVersion(ctx context.Context, pool *pgxpool.Pool, tenantID, versionID uuid.UUID) (*TemplateVersion, error) {
	var v TemplateVersion
	err := pool.QueryRow(ctx, `
		SELECT v.id, v.template_id, v.parent_id, v.source, v.summary, v.author, v.created_by, v.created_at
		FROM app_poster_template_versions v JOIN app_poster_templates t ON t.id = v.template_id
		WHERE (t.tenant_id = $1 OR t.tenant_id IS NULL) AND v.id = $2`, tenantID, versionID).
		Scan(&v.ID, &v.TemplateID, &v.ParentID, &v.Source, &v.Summary, &v.Author, &v.CreatedBy, &v.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("loading template version: %w", err)
	}
	return &v, nil
}

func setTemplateStatus(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID, status TemplateStatus) error {
	tag, err := pool.Exec(ctx, `UPDATE app_poster_templates SET status = $3, updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenantID, id, string(status))
	if err != nil {
		return fmt.Errorf("setting template status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func renameTemplate(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID, name string) error {
	tag, err := pool.Exec(ctx, `UPDATE app_poster_templates SET name = $3, updated_at = now() WHERE tenant_id = $1 AND id = $2`, tenantID, id, name)
	if err != nil {
		return fmt.Errorf("renaming template: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func setTemplateHidden(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID, hidden bool) error {
	var err error
	if hidden {
		_, err = pool.Exec(ctx, `INSERT INTO app_poster_template_hidden (tenant_id, template_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, tenantID, id)
	} else {
		_, err = pool.Exec(ctx, `DELETE FROM app_poster_template_hidden WHERE tenant_id = $1 AND template_id = $2`, tenantID, id)
	}
	if err != nil {
		return fmt.Errorf("hiding template: %w", err)
	}
	return nil
}

// templateEdits is the "usually edited to..." summary: the instructions
// behind agent edits that followed a pick of this template, newest first.
func templateEdits(ctx context.Context, pool *pgxpool.Pool, tenantID, templateID uuid.UUID, limit int) ([]string, error) {
	rows, err := pool.Query(ctx, `
		SELECT v.instruction FROM app_poster_versions v
		WHERE v.tenant_id = $1 AND v.template_id = $2 AND v.author = 'agent' AND v.instruction <> ''
		ORDER BY v.created_at DESC LIMIT $3`, tenantID, templateID, limit)
	if err != nil {
		return nil, fmt.Errorf("listing template edits: %w", err)
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

// upsertBuiltin installs or refreshes one built-in template. The source is
// versioned like any other so a tenant copy can be traced to the version it
// came from; an unchanged source adds no version.
func upsertBuiltin(ctx context.Context, pool *pgxpool.Pool, key string, meta posterrender.TemplateMeta, source string) error {
	metaJSON, _ := json.Marshal(meta)
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("installing built-in template: %w", err)
	}
	defer tx.Rollback(ctx)
	var id uuid.UUID
	var current *uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO app_poster_templates (tenant_id, builtin_key, name, description, status, origin, meta)
		VALUES (NULL, $1, $2, $3, $5, $6, $4)
		ON CONFLICT (builtin_key) WHERE tenant_id IS NULL DO UPDATE SET name = EXCLUDED.name, description = EXCLUDED.description, meta = EXCLUDED.meta, updated_at = now()
		RETURNING id, current_version_id`, key, meta.Name, meta.Description, metaJSON, TemplateActive, OriginBuiltin).Scan(&id, &current); err != nil {
		return fmt.Errorf("installing built-in template %s: %w", key, err)
	}
	if current != nil {
		var existing string
		if err := tx.QueryRow(ctx, `SELECT source FROM app_poster_template_versions WHERE tenant_id IS NULL AND id = $1`, *current).Scan(&existing); err == nil && existing == source {
			return tx.Commit(ctx)
		}
	}
	var vid uuid.UUID
	if err := tx.QueryRow(ctx, `
		INSERT INTO app_poster_template_versions (tenant_id, template_id, parent_id, source, summary, author)
		VALUES (NULL, $1, $2, $3, 'Shipped with Kit', 'builtin') RETURNING id`, id, current, source).Scan(&vid); err != nil {
		return fmt.Errorf("versioning built-in template %s: %w", key, err)
	}
	if _, err := tx.Exec(ctx, `UPDATE app_poster_templates SET current_version_id = $2 WHERE id = $1 AND tenant_id IS NULL`, id, vid); err != nil {
		return fmt.Errorf("pointing built-in template at its version: %w", err)
	}
	return tx.Commit(ctx)
}
