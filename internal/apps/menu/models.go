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
}

const boardColumns = `id, tenant_id, key, name, payload, created_at, updated_at`

func scanBoardRow(row pgx.Row) (*BoardRow, error) {
	var b BoardRow
	err := row.Scan(&b.ID, &b.TenantID, &b.Key, &b.Name, &b.Payload, &b.CreatedAt, &b.UpdatedAt)
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
