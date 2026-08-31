package menu

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AssetRef is the prefix a panel uses to point at a stored image, as in
// "asset:anniversary". The alternative — a bare key — could not be told apart
// from a data URI without guessing.
const AssetRef = "asset:"

// Asset is one stored image. Size is carried separately from Bytes because
// the listing path asks Postgres for octet_length rather than dragging every
// image through the process to count them.
type Asset struct {
	Key       string
	Mime      string
	Bytes     []byte
	Size      int
	SourceURL string
	UpdatedAt time.Time
}

// DataURI renders the asset for inlining into a page.
func (a *Asset) DataURI() string {
	return "data:" + a.Mime + ";base64," + base64.StdEncoding.EncodeToString(a.Bytes)
}

// UpsertAsset stores an image, replacing any existing one with the same key.
func UpsertAsset(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, a *Asset) error {
	const q = `INSERT INTO app_menu_assets (tenant_id, key, mime, bytes, source_url)
	           VALUES ($1, $2, $3, $4, $5)
	           ON CONFLICT (tenant_id, key) DO UPDATE
	             SET mime = EXCLUDED.mime, bytes = EXCLUDED.bytes,
	                 source_url = EXCLUDED.source_url, updated_at = NOW()`
	if _, err := pool.Exec(ctx, q, tenantID, a.Key, a.Mime, a.Bytes, a.SourceURL); err != nil {
		return fmt.Errorf("upserting menu asset: %w", err)
	}
	return nil
}

// LoadAssets returns every asset for a tenant as key -> data URI.
//
// All of them, in one query, rather than one lookup per panel: a workspace has
// a handful of small images and the render path is not hot, so the simpler
// shape is worth more than the bytes saved by being selective.
func LoadAssets(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (map[string]string, error) {
	const q = `SELECT key, mime, bytes FROM app_menu_assets WHERE tenant_id = $1`
	rows, err := pool.Query(ctx, q, tenantID)
	if err != nil {
		return nil, fmt.Errorf("querying menu assets: %w", err)
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var a Asset
		if err := rows.Scan(&a.Key, &a.Mime, &a.Bytes); err != nil {
			return nil, fmt.Errorf("scanning menu asset: %w", err)
		}
		out[a.Key] = a.DataURI()
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating menu assets: %w", err)
	}
	return out, nil
}

// ListAssetKeys returns the tenant's asset keys with their sizes, for the
// listing tool.
func ListAssetKeys(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) ([]Asset, error) {
	const q = `SELECT key, mime, octet_length(bytes), source_url, updated_at
	           FROM app_menu_assets WHERE tenant_id = $1 ORDER BY key`
	rows, err := pool.Query(ctx, q, tenantID)
	if err != nil {
		return nil, fmt.Errorf("querying menu asset keys: %w", err)
	}
	defer rows.Close()
	var out []Asset
	for rows.Next() {
		var a Asset
		if err := rows.Scan(&a.Key, &a.Mime, &a.Size, &a.SourceURL, &a.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning menu asset key: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating menu asset keys: %w", err)
	}
	return out, nil
}
