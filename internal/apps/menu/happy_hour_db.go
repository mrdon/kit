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

	// Live is the last Start now / End now, nil when none was ever pressed.
	Live *HappyLive

	// SquareIDs are the catalog objects Square holds for happy hour now.
	SquareIDs []string
	// SyncedHash is the Square state the last sync aimed for (squareKey),
	// recorded whether or not it got there, so a failing sync is retried on a
	// timer rather than every minute.
	SyncedHash string
	SyncedAt   *time.Time
	SyncLog    string
	SyncOK     bool
	UpdatedAt  time.Time
}

// OnAt reports whether happy hour is on at now.
func (s *HappyHourState) OnAt(now time.Time, loc *time.Location) bool {
	return s.Configured && s.Config.OnAt(s.Live, now, loc)
}

// squareKey is the Square state wanted at now: "off", or "on:" plus the
// setting's hash, so changing the beers while it is on is a change too.
func (s *HappyHourState) squareKey(now time.Time, loc *time.Location) string {
	if !s.OnAt(now, loc) {
		return "off"
	}
	return "on:" + s.Config.Hash()
}

// InSync reports whether Square holds what it should at now.
func (s *HappyHourState) InSync(now time.Time, loc *time.Location) bool {
	return s.SyncOK && s.SyncedHash == s.squareKey(now, loc)
}

// LoadHappyHour reads the workspace's happy hour. A workspace with none gets
// the default setting, switched off, rather than an error.
func LoadHappyHour(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (*HappyHourState, error) {
	const q = `SELECT config, live, square_ids, synced_hash, synced_at, sync_log, sync_ok, updated_at
	           FROM app_menu_happy_hour WHERE tenant_id = $1`
	var (
		s                    HappyHourState
		cfgRaw, liveRaw, ids []byte
	)
	err := pool.QueryRow(ctx, q, tenantID).Scan(&cfgRaw, &liveRaw, &ids, &s.SyncedHash,
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
	if len(liveRaw) > 0 {
		s.Live = &HappyLive{}
		if err := json.Unmarshal(liveRaw, s.Live); err != nil {
			return nil, fmt.Errorf("decoding happy hour live state: %w", err)
		}
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

// SaveHappyLive records a Start now or End now. The row must already exist:
// there is nothing to start before a happy hour has been set up.
func SaveHappyLive(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, live HappyLive) error {
	raw, err := json.Marshal(live)
	if err != nil {
		return fmt.Errorf("encoding happy hour live state: %w", err)
	}
	const q = `UPDATE app_menu_happy_hour SET live = $2, updated_at = NOW() WHERE tenant_id = $1`
	tag, err := pool.Exec(ctx, q, tenantID, raw)
	if err != nil {
		return fmt.Errorf("saving happy hour live state: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: set up a happy hour first", ErrPayloadInvalid)
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
	const q = `UPDATE app_menu_happy_hour
	           SET square_ids = $2, synced_hash = $3, synced_at = NOW(),
	               sync_log = $4, sync_ok = $5
	           WHERE tenant_id = $1`
	if _, err := pool.Exec(ctx, q, tenantID, rawIDs, hash, log, ok); err != nil {
		return fmt.Errorf("recording happy hour sync: %w", err)
	}
	return nil
}
