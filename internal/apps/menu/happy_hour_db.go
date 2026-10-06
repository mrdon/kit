package menu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// HappyHourState is the stored row: the setting, plus what the last Square
// sync left behind.
type HappyHourState struct {
	Config HappyHour

	// Configured is false for a workspace that has never saved a happy hour,
	// in which case Config is DefaultHappyHour and nothing is shown anywhere.
	Configured bool

	// SquareIDs are the catalog objects the last successful sync created.
	SquareIDs  []string
	SyncedHash string
	SyncedAt   *time.Time
	SyncLog    string
	SyncOK     bool
	UpdatedAt  time.Time
}

// InSync reports whether Square was last built from the setting as it is now.
func (s *HappyHourState) InSync() bool {
	return s.SyncOK && s.SyncedHash == s.Config.Hash()
}

// LoadHappyHour reads the workspace's happy hour. A workspace with none gets
// the default setting, switched off, rather than an error.
func LoadHappyHour(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (*HappyHourState, error) {
	const q = `SELECT config, square_ids, synced_hash, synced_at, sync_log, sync_ok, updated_at
	           FROM app_menu_happy_hour WHERE tenant_id = $1`
	var (
		s           HappyHourState
		cfgRaw, ids []byte
	)
	err := pool.QueryRow(ctx, q, tenantID).Scan(&cfgRaw, &ids, &s.SyncedHash,
		&s.SyncedAt, &s.SyncLog, &s.SyncOK, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return &HappyHourState{Config: DefaultHappyHour(), SquareIDs: []string{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("loading happy hour: %w", err)
	}
	s.Config = DefaultHappyHour()
	if err := json.Unmarshal(cfgRaw, &s.Config); err != nil {
		return nil, fmt.Errorf("decoding happy hour: %w", err)
	}
	if err := json.Unmarshal(ids, &s.SquareIDs); err != nil {
		return nil, fmt.Errorf("decoding happy hour square ids: %w", err)
	}
	s.Configured = true
	return &s, nil
}

// SaveHappyHour writes the setting. It does not touch Square: that is a sync,
// done on purpose and in front of somebody, never as a side effect of a save.
func SaveHappyHour(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, h HappyHour) error {
	raw, err := json.Marshal(h)
	if err != nil {
		return fmt.Errorf("encoding happy hour: %w", err)
	}
	const q = `INSERT INTO app_menu_happy_hour (tenant_id, config)
	           VALUES ($1, $2)
	           ON CONFLICT (tenant_id) DO UPDATE
	             SET config = EXCLUDED.config, updated_at = NOW()`
	if _, err := pool.Exec(ctx, q, tenantID, raw); err != nil {
		return fmt.Errorf("saving happy hour: %w", err)
	}
	return nil
}

// RecordHappyHourSync stores the outcome of a Square sync. ids is what Square
// now holds for happy hour: the new objects on success, and on failure
// whatever the sync left standing, so the next attempt cleans it up.
func RecordHappyHourSync(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID,
	ids []string, hash, log string, ok bool,
) error {
	if ids == nil {
		ids = []string{}
	}
	rawIDs, err := json.Marshal(ids)
	if err != nil {
		return fmt.Errorf("encoding square ids: %w", err)
	}
	if !ok {
		hash = ""
	}
	const q = `UPDATE app_menu_happy_hour
	           SET square_ids = $2, synced_hash = $3, synced_at = NOW(),
	               sync_log = $4, sync_ok = $5
	           WHERE tenant_id = $1`
	if _, err := pool.Exec(ctx, q, tenantID, rawIDs, hash, log, ok); err != nil {
		return fmt.Errorf("recording happy hour sync: %w", err)
	}
	return nil
}
