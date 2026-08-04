package squareshifts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// mapping is one row of app_squareshifts_map: a Square shift ↔ Google event
// link plus the content hash used to skip unchanged shifts.
type mapping struct {
	ShiftID       string
	GoogleEventID string
	StartAt       time.Time
	Version       int
	ContentHash   string
}

// getMapping returns the mapping for a shift, or (nil, nil) if none.
func getMapping(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, shiftID string) (*mapping, error) {
	m := &mapping{}
	err := pool.QueryRow(ctx, `
		SELECT shift_id, google_event_id, start_at, version, content_hash
		FROM app_squareshifts_map
		WHERE tenant_id = $1 AND shift_id = $2`,
		tenantID, shiftID,
	).Scan(&m.ShiftID, &m.GoogleEventID, &m.StartAt, &m.Version, &m.ContentHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // not found is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("loading shift mapping: %w", err)
	}
	return m, nil
}

// upsertMapping records (or updates) a shift ↔ event link.
func upsertMapping(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, shiftID, eventID string, startAt time.Time, version int, contentHash string) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO app_squareshifts_map
			(tenant_id, shift_id, google_event_id, start_at, version, content_hash, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, now())
		ON CONFLICT (tenant_id, shift_id) DO UPDATE SET
			google_event_id = EXCLUDED.google_event_id,
			start_at        = EXCLUDED.start_at,
			version         = EXCLUDED.version,
			content_hash    = EXCLUDED.content_hash,
			updated_at      = now()`,
		tenantID, shiftID, eventID, startAt, version, contentHash,
	)
	if err != nil {
		return fmt.Errorf("upserting shift mapping: %w", err)
	}
	return nil
}

// deleteMapping removes a shift ↔ event link.
func deleteMapping(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, shiftID string) error {
	_, err := pool.Exec(ctx,
		`DELETE FROM app_squareshifts_map WHERE tenant_id = $1 AND shift_id = $2`,
		tenantID, shiftID,
	)
	if err != nil {
		return fmt.Errorf("deleting shift mapping: %w", err)
	}
	return nil
}

// listMappingsInWindow returns mappings whose shift start falls in
// [start, end) — the candidates for delete-detection. Past events (start <
// window) are excluded so they're never pruned as "vanished".
func listMappingsInWindow(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, start, end time.Time) ([]mapping, error) {
	rows, err := pool.Query(ctx, `
		SELECT shift_id, google_event_id, start_at, version, content_hash
		FROM app_squareshifts_map
		WHERE tenant_id = $1 AND start_at >= $2 AND start_at < $3`,
		tenantID, start, end,
	)
	if err != nil {
		return nil, fmt.Errorf("listing shift mappings: %w", err)
	}
	defer rows.Close()
	var out []mapping
	for rows.Next() {
		var m mapping
		if err := rows.Scan(&m.ShiftID, &m.GoogleEventID, &m.StartAt, &m.Version, &m.ContentHash); err != nil {
			return nil, fmt.Errorf("scanning shift mapping: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
