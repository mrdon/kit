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

// BoardRow is a workspace's menu. Exactly one row per tenant — the schema
// enforces it — so every lookup here is by tenant alone.
//
// Payload is the raw JSON document; it is decoded on render rather than on
// load, so a board that fails to parse still shows in the console instead of
// breaking the page that would let someone fix it.
type BoardRow struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	Name      string
	Payload   []byte
	CreatedAt time.Time
	UpdatedAt time.Time

	// SourceKind is "untappd" when the tap list is pulled rather than set by
	// hand; empty means nothing upstream and no refresh touches it.
	SourceKind string
	SourceID   string
	SourceHash string
	SyncedAt   *time.Time
	SyncError  string
}

const boardColumns = `id, tenant_id, name, payload, created_at, updated_at, ` +
	`source_kind, source_id, source_hash, synced_at, sync_error`

func scanBoardRow(row pgx.Row) (*BoardRow, error) {
	var b BoardRow
	err := row.Scan(&b.ID, &b.TenantID, &b.Name, &b.Payload, &b.CreatedAt, &b.UpdatedAt,
		&b.SourceKind, &b.SourceID, &b.SourceHash, &b.SyncedAt, &b.SyncError)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// GetBoard loads the workspace's menu, or nil when no tap list has been set.
func GetBoard(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (*BoardRow, error) {
	q := `SELECT ` + boardColumns + ` FROM app_menu_boards WHERE tenant_id = $1`
	b, err := scanBoardRow(pool.QueryRow(ctx, q, tenantID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil //nolint:nilnil // explicit "not set yet" sentinel
		}
		return nil, fmt.Errorf("querying menu board: %w", err)
	}
	return b, nil
}

// UpsertBoard writes the workspace's menu, creating it on first use.
//
// The source columns are deliberately untouched: setting a tap list by hand
// is not a statement about where tap lists come from, and clobbering them
// here would silently switch off the Untappd sync.
func UpsertBoard(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, name string, payload []byte) (*BoardRow, error) {
	q := `INSERT INTO app_menu_boards (tenant_id, name, payload)
	      VALUES ($1, $2, $3)
	      ON CONFLICT (tenant_id) DO UPDATE
	        SET name = EXCLUDED.name, payload = EXCLUDED.payload, updated_at = NOW()
	      RETURNING ` + boardColumns
	out, err := scanBoardRow(pool.QueryRow(ctx, q, tenantID, name, payload))
	if err != nil {
		return nil, fmt.Errorf("upserting menu board: %w", err)
	}
	return out, nil
}

// SetBoardSource points the menu at an upstream tap list, or clears it when
// kind is empty. It creates the row when the workspace has no menu yet, so a
// source can be configured before any tap list exists — which is the normal
// order, since configuring the source is what produces the first one.
func SetBoardSource(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, kind, sourceID string) (*BoardRow, error) {
	q := `INSERT INTO app_menu_boards (tenant_id, name, payload, source_kind, source_id)
	      VALUES ($1, 'Menu', '{}'::jsonb, $2, $3)
	      ON CONFLICT (tenant_id) DO UPDATE
	        SET source_kind = EXCLUDED.source_kind,
	            source_id   = EXCLUDED.source_id,
	            -- Clear the hash so the next look upstream actually pulls,
	            -- rather than trusting one taken from a different board.
	            source_hash = '',
	            updated_at  = NOW()
	      RETURNING ` + boardColumns
	b, err := scanBoardRow(pool.QueryRow(ctx, q, tenantID, kind, sourceID))
	if err != nil {
		return nil, fmt.Errorf("setting menu source: %w", err)
	}
	return b, nil
}

// SaveSyncedTaps writes a freshly pulled tap list and stamps the outcome.
func SaveSyncedTaps(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, payload []byte, hash string) error {
	const q = `UPDATE app_menu_boards
	           SET payload = $2, source_hash = $3, synced_at = NOW(),
	               sync_error = '', updated_at = NOW()
	           WHERE tenant_id = $1`
	if _, err := pool.Exec(ctx, q, tenantID, payload, hash); err != nil {
		return fmt.Errorf("saving synced taps: %w", err)
	}
	return nil
}

// TouchSynced records a successful pull that found nothing new: the upstream
// hash and the timestamp move, the payload does not, so updated_at keeps
// meaning "when the tap list last actually changed".
func TouchSynced(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, hash string) error {
	const q = `UPDATE app_menu_boards
	           SET source_hash = $2, synced_at = NOW(), sync_error = ''
	           WHERE tenant_id = $1`
	if _, err := pool.Exec(ctx, q, tenantID, hash); err != nil {
		return fmt.Errorf("touching menu sync: %w", err)
	}
	return nil
}

// RecordSyncError stamps a failed pull WITHOUT touching the payload. The
// board keeps showing the last good tap list: stale beer is recoverable, a
// blank wall in a full taproom is not.
func RecordSyncError(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, msg string) error {
	const q = `UPDATE app_menu_boards SET sync_error = $2, updated_at = NOW()
	           WHERE tenant_id = $1`
	if _, err := pool.Exec(ctx, q, tenantID, msg); err != nil {
		return fmt.Errorf("recording sync error: %w", err)
	}
	return nil
}

// HasBoard reports whether the workspace has a menu row at all. Used for the
// Apps settings usage line, which wants a cheap yes/no rather than a count of
// something there can only ever be one of.
func HasBoard(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (bool, error) {
	const q = `SELECT EXISTS(SELECT 1 FROM app_menu_boards WHERE tenant_id = $1)`
	var ok bool
	if err := pool.QueryRow(ctx, q, tenantID).Scan(&ok); err != nil {
		return false, fmt.Errorf("checking for a menu board: %w", err)
	}
	return ok, nil
}
