package menu

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// BoardRow is one stored menu board. Payload is the raw JSON document; it is
// decoded on render rather than on load, so a board that fails to parse still
// lists in the console instead of breaking the page that would let someone
// fix it.
type BoardRow struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	Key       string
	Name      string
	Payload   []byte
	CreatedAt time.Time
	UpdatedAt time.Time

	// SourceKind is "untappd" when the tap list is pulled rather than pushed;
	// empty means the payload is authored by hand and no sync touches it.
	SourceKind string
	SourceID   string
	SourceHash string
	SyncedAt   *time.Time
	SyncError  string
}

const boardColumns = `id, tenant_id, key, name, payload, created_at, updated_at, source_kind, source_id, source_hash, synced_at, sync_error`

func scanBoardRow(row pgx.Row) (*BoardRow, error) {
	var b BoardRow
	err := row.Scan(&b.ID, &b.TenantID, &b.Key, &b.Name, &b.Payload, &b.CreatedAt, &b.UpdatedAt,
		&b.SourceKind, &b.SourceID, &b.SourceHash, &b.SyncedAt, &b.SyncError)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// ListBoards returns the tenant's boards ordered by name.
func ListBoards(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) ([]*BoardRow, error) {
	q := `SELECT ` + boardColumns + ` FROM app_menu_boards WHERE tenant_id = $1 ORDER BY name`
	rows, err := pool.Query(ctx, q, tenantID)
	if err != nil {
		return nil, fmt.Errorf("querying app_menu_boards: %w", err)
	}
	defer rows.Close()
	var out []*BoardRow
	for rows.Next() {
		b, err := scanBoardRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning menu board: %w", err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating menu boards: %w", err)
	}
	return out, nil
}

// GetBoardByKey loads one board by its stable key, or nil when the tenant has
// no board by that key.
func GetBoardByKey(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, key string) (*BoardRow, error) {
	q := `SELECT ` + boardColumns + ` FROM app_menu_boards WHERE tenant_id = $1 AND key = $2`
	b, err := scanBoardRow(pool.QueryRow(ctx, q, tenantID, key))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil //nolint:nilnil // explicit "not found" sentinel
		}
		return nil, fmt.Errorf("querying menu board by key: %w", err)
	}
	return b, nil
}

// UpsertBoard writes a board, replacing any existing one with the same key.
// Authoring pushes the whole document, so an upsert is the honest primitive:
// there is no field-level edit that a separate insert and update would serve.
func UpsertBoard(ctx context.Context, pool *pgxpool.Pool, b *BoardRow) (*BoardRow, error) {
	q := `INSERT INTO app_menu_boards (tenant_id, key, name, payload)
	      VALUES ($1, $2, $3, $4)
	      ON CONFLICT (tenant_id, key) DO UPDATE
	        SET name = EXCLUDED.name, payload = EXCLUDED.payload, updated_at = NOW()
	      RETURNING ` + boardColumns
	out, err := scanBoardRow(pool.QueryRow(ctx, q, b.TenantID, b.Key, b.Name, b.Payload))
	if err != nil {
		return nil, fmt.Errorf("upserting menu board: %w", err)
	}
	return out, nil
}

// DeleteBoard removes a board, reporting whether a row was actually deleted.
func DeleteBoard(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, key string) (bool, error) {
	const q = `DELETE FROM app_menu_boards WHERE tenant_id = $1 AND key = $2`
	tag, err := pool.Exec(ctx, q, tenantID, key)
	if err != nil {
		return false, fmt.Errorf("deleting menu board: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// CountBoards is the usage summary's single indexed count.
func CountBoards(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (int, error) {
	const q = `SELECT COUNT(*) FROM app_menu_boards WHERE tenant_id = $1`
	var n int
	if err := pool.QueryRow(ctx, q, tenantID).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting menu boards: %w", err)
	}
	return n, nil
}

// SetBoardSource points a board at an upstream tap list, or clears it when
// kind is empty.
func SetBoardSource(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, key, kind, sourceID string) (*BoardRow, error) {
	q := `UPDATE app_menu_boards
	      SET source_kind = $3, source_id = $4, updated_at = NOW()
	      WHERE tenant_id = $1 AND key = $2
	      RETURNING ` + boardColumns
	b, err := scanBoardRow(pool.QueryRow(ctx, q, tenantID, key, kind, sourceID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("setting menu board source: %w", err)
	}
	return b, nil
}

// SaveSyncedTaps writes a freshly pulled tap list and stamps the outcome.
//
// The payload is rewritten wholesale but only its `taps` key changes — the
// caller merges — so a sync can never drop the venue chrome or the panels,
// which have no upstream to be restored from.
func SaveSyncedTaps(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, key string, payload []byte, hash string) error {
	const q = `UPDATE app_menu_boards
	           SET payload = $3, source_hash = $4, synced_at = NOW(),
	               sync_error = '', updated_at = NOW()
	           WHERE tenant_id = $1 AND key = $2`
	if _, err := pool.Exec(ctx, q, tenantID, key, payload, hash); err != nil {
		return fmt.Errorf("saving synced taps: %w", err)
	}
	return nil
}

// TouchSynced records a successful pull that found nothing new: the upstream
// hash and the timestamp move, the payload does not.
func TouchSynced(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, key, hash string) error {
	const q = `UPDATE app_menu_boards
	           SET source_hash = $3, synced_at = NOW(), sync_error = ''
	           WHERE tenant_id = $1 AND key = $2`
	if _, err := pool.Exec(ctx, q, tenantID, key, hash); err != nil {
		return fmt.Errorf("touching menu sync: %w", err)
	}
	return nil
}

// RecordSyncError stamps a failed pull WITHOUT touching the payload. The
// board keeps showing the last good tap list: stale beer is recoverable, a
// blank wall in a full taproom is not.
func RecordSyncError(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, key, msg string) error {
	const q = `UPDATE app_menu_boards SET sync_error = $3, updated_at = NOW()
	           WHERE tenant_id = $1 AND key = $2`
	if _, err := pool.Exec(ctx, q, tenantID, key, msg); err != nil {
		return fmt.Errorf("recording sync error: %w", err)
	}
	return nil
}

// ListSourcedBoards returns every board with an upstream, for the sync pass.
func ListSourcedBoards(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) ([]*BoardRow, error) {
	q := `SELECT ` + boardColumns + ` FROM app_menu_boards
	      WHERE tenant_id = $1 AND source_kind <> '' ORDER BY key`
	rows, err := pool.Query(ctx, q, tenantID)
	if err != nil {
		return nil, fmt.Errorf("querying sourced menu boards: %w", err)
	}
	defer rows.Close()
	var out []*BoardRow
	for rows.Next() {
		b, err := scanBoardRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning sourced menu board: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
