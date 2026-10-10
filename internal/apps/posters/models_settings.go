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

// LogoFile is one mapped logo variant: the Drive file that holds it.
type LogoFile struct {
	FileID   string `json:"file_id"`
	Name     string `json:"name"`
	Modified string `json:"modified"`
}

// Settings is the per-tenant configuration row plus the cached brand. A
// tenant with no row gets the zero value, so the app answers before anyone
// visits the admin page.
type Settings struct {
	TenantID         uuid.UUID           `json:"tenant_id"`
	PhotoFolderID    string              `json:"photo_folder_id"`
	LogoFolderID     string              `json:"logo_folder_id"`
	LogoMap          map[string]LogoFile `json:"logo_map"`
	AllowStockPhotos bool                `json:"allow_stock_photos"`
	Brand            *posterrender.Brand `json:"brand,omitempty"`
	BrandHash        string              `json:"brand_hash"`
	BrandProblems    []string            `json:"brand_problems"`
	BrandDerivedAt   *time.Time          `json:"brand_derived_at,omitempty"`
	LastSyncAt       *time.Time          `json:"last_sync_at,omitempty"`
	LastSyncError    string              `json:"last_sync_error"`
	UpdatedAt        time.Time           `json:"updated_at"`
}

const settingsColumns = `tenant_id, photo_folder_id, logo_folder_id, logo_map, allow_stock_photos,
	brand_json, brand_hash, brand_problems, brand_derived_at, last_sync_at, last_sync_error, updated_at`

func scanSettings(row pgx.Row) (Settings, error) {
	var s Settings
	var logoMap, brand []byte
	err := row.Scan(&s.TenantID, &s.PhotoFolderID, &s.LogoFolderID, &logoMap, &s.AllowStockPhotos,
		&brand, &s.BrandHash, &s.BrandProblems, &s.BrandDerivedAt, &s.LastSyncAt, &s.LastSyncError, &s.UpdatedAt)
	if err != nil {
		return Settings{}, err
	}
	s.LogoMap = map[string]LogoFile{}
	if len(logoMap) > 0 {
		if err := json.Unmarshal(logoMap, &s.LogoMap); err != nil {
			return Settings{}, fmt.Errorf("decoding logo map: %w", err)
		}
	}
	if len(brand) > 0 {
		var b posterrender.Brand
		if err := json.Unmarshal(brand, &b); err != nil {
			return Settings{}, fmt.Errorf("decoding brand: %w", err)
		}
		s.Brand = &b
	}
	if s.BrandProblems == nil {
		s.BrandProblems = []string{}
	}
	return s, nil
}

func getSettings(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (Settings, error) {
	s, err := scanSettings(pool.QueryRow(ctx, `SELECT `+settingsColumns+` FROM app_poster_settings WHERE tenant_id = $1`, tenantID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Settings{TenantID: tenantID, LogoMap: map[string]LogoFile{}, BrandProblems: []string{}}, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("loading poster settings: %w", err)
	}
	return s, nil
}

// upsertSettings writes the admin-editable fields. The cached brand and
// sync state have their own writers so a settings save never clobbers them.
func upsertSettings(ctx context.Context, pool *pgxpool.Pool, s Settings) (Settings, error) {
	logoMap, err := json.Marshal(s.LogoMap)
	if err != nil {
		return Settings{}, fmt.Errorf("encoding logo map: %w", err)
	}
	out, err := scanSettings(pool.QueryRow(ctx, `
		INSERT INTO app_poster_settings (tenant_id, photo_folder_id, logo_folder_id, logo_map, allow_stock_photos, updated_at)
		VALUES ($1, $2, $3, $4, $5, now())
		ON CONFLICT (tenant_id) DO UPDATE SET
			photo_folder_id = EXCLUDED.photo_folder_id,
			logo_folder_id = EXCLUDED.logo_folder_id,
			logo_map = EXCLUDED.logo_map,
			allow_stock_photos = EXCLUDED.allow_stock_photos,
			updated_at = now()
		RETURNING `+settingsColumns,
		s.TenantID, s.PhotoFolderID, s.LogoFolderID, logoMap, s.AllowStockPhotos))
	if err != nil {
		return Settings{}, fmt.Errorf("saving poster settings: %w", err)
	}
	return out, nil
}

// setBrand caches a derived brand (or the problems that stopped it).
func setBrand(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, brand *posterrender.Brand, hash string, problems []string) error {
	var raw []byte
	if brand != nil {
		var err error
		if raw, err = json.Marshal(brand); err != nil {
			return fmt.Errorf("encoding brand: %w", err)
		}
	}
	if problems == nil {
		problems = []string{}
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO app_poster_settings (tenant_id, brand_json, brand_hash, brand_problems, brand_derived_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (tenant_id) DO UPDATE SET
			brand_json = EXCLUDED.brand_json,
			brand_hash = EXCLUDED.brand_hash,
			brand_problems = EXCLUDED.brand_problems,
			brand_derived_at = now()`,
		tenantID, raw, hash, problems)
	if err != nil {
		return fmt.Errorf("caching brand: %w", err)
	}
	return nil
}

func setSyncResult(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, syncErr string) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO app_poster_settings (tenant_id, last_sync_at, last_sync_error)
		VALUES ($1, now(), $2)
		ON CONFLICT (tenant_id) DO UPDATE SET last_sync_at = now(), last_sync_error = EXCLUDED.last_sync_error`,
		tenantID, syncErr)
	if err != nil {
		return fmt.Errorf("recording photo sync: %w", err)
	}
	return nil
}
