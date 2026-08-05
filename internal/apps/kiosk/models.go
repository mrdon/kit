package kiosk

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Board is one physical screen. Key is the half baked into the kiosk's
// browser homepage, so it is stable by contract; Name is the human label
// and may change freely. URL is empty for a board nobody has assigned
// content to yet.
type Board struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	Key        string
	Name       string
	URL        string
	Notes      string
	LastSeenAt *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

const boardColumns = `id, tenant_id, key, name, COALESCE(url, ''), notes, last_seen_at, created_at, updated_at`

func scanBoard(row pgx.Row) (*Board, error) {
	var b Board
	err := row.Scan(&b.ID, &b.TenantID, &b.Key, &b.Name, &b.URL, &b.Notes,
		&b.LastSeenAt, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// ListBoards returns the tenant's boards ordered by name.
func ListBoards(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) ([]*Board, error) {
	q := `SELECT ` + boardColumns + ` FROM app_kiosk_boards WHERE tenant_id = $1 ORDER BY name`
	rows, err := pool.Query(ctx, q, tenantID)
	if err != nil {
		return nil, fmt.Errorf("querying app_kiosk_boards: %w", err)
	}
	defer rows.Close()
	var out []*Board
	for rows.Next() {
		b, err := scanBoard(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning kiosk board: %w", err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating kiosk boards: %w", err)
	}
	return out, nil
}

// GetBoardByKey loads one board by its stable key, or nil when the tenant
// has no board by that key.
func GetBoardByKey(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, key string) (*Board, error) {
	q := `SELECT ` + boardColumns + ` FROM app_kiosk_boards WHERE tenant_id = $1 AND key = $2`
	b, err := scanBoard(pool.QueryRow(ctx, q, tenantID, key))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil //nolint:nilnil // explicit "not found" sentinel
		}
		return nil, fmt.Errorf("querying kiosk board by key: %w", err)
	}
	return b, nil
}

// GetBoard loads one board by id, or nil when it doesn't exist for the tenant.
func GetBoard(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID) (*Board, error) {
	q := `SELECT ` + boardColumns + ` FROM app_kiosk_boards WHERE tenant_id = $1 AND id = $2`
	b, err := scanBoard(pool.QueryRow(ctx, q, tenantID, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil //nolint:nilnil // explicit "not found" sentinel
		}
		return nil, fmt.Errorf("querying kiosk board: %w", err)
	}
	return b, nil
}

// InsertBoard creates a board. A duplicate key for the tenant surfaces as
// ErrKeyTaken so the handler can return 409 instead of a 500.
func InsertBoard(ctx context.Context, pool *pgxpool.Pool, b *Board) (*Board, error) {
	q := `INSERT INTO app_kiosk_boards (tenant_id, key, name, url, notes)
	      VALUES ($1, $2, $3, NULLIF($4, ''), $5)
	      RETURNING ` + boardColumns
	out, err := scanBoard(pool.QueryRow(ctx, q, b.TenantID, b.Key, b.Name, b.URL, b.Notes))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrKeyTaken
		}
		return nil, fmt.Errorf("inserting kiosk board: %w", err)
	}
	return out, nil
}

// UpdateBoard writes every mutable field of an existing board and bumps
// updated_at. Returns nil when the row doesn't exist for this tenant.
func UpdateBoard(ctx context.Context, pool *pgxpool.Pool, b *Board) (*Board, error) {
	q := `UPDATE app_kiosk_boards
	      SET key = $3, name = $4, url = NULLIF($5, ''), notes = $6, updated_at = NOW()
	      WHERE tenant_id = $1 AND id = $2
	      RETURNING ` + boardColumns
	out, err := scanBoard(pool.QueryRow(ctx, q, b.TenantID, b.ID, b.Key, b.Name, b.URL, b.Notes))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil //nolint:nilnil // explicit "not found" sentinel
		}
		if isUniqueViolation(err) {
			return nil, ErrKeyTaken
		}
		return nil, fmt.Errorf("updating kiosk board: %w", err)
	}
	return out, nil
}

// DeleteBoard removes a board. Reports whether a row was actually deleted so
// the handler can 404 on an unknown id.
func DeleteBoard(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID) (bool, error) {
	const q = `DELETE FROM app_kiosk_boards WHERE tenant_id = $1 AND id = $2`
	tag, err := pool.Exec(ctx, q, tenantID, id)
	if err != nil {
		return false, fmt.Errorf("deleting kiosk board: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// TouchBoardSeen records that a kiosk polled this board. Called on the hot
// path of every poll, so it is a single indexed UPDATE and its error is
// advisory — callers log and carry on.
func TouchBoardSeen(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID) error {
	const q = `UPDATE app_kiosk_boards SET last_seen_at = NOW() WHERE tenant_id = $1 AND id = $2`
	if _, err := pool.Exec(ctx, q, tenantID, id); err != nil {
		return fmt.Errorf("touching kiosk board last_seen_at: %w", err)
	}
	return nil
}

// CountBoards is the usage summary's single indexed count.
func CountBoards(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (int, error) {
	const q = `SELECT COUNT(*) FROM app_kiosk_boards WHERE tenant_id = $1`
	var n int
	if err := pool.QueryRow(ctx, q, tenantID).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting kiosk boards: %w", err)
	}
	return n, nil
}

// isUniqueViolation matches pgx's representation of SQLSTATE 23505 — here,
// two boards claiming the same key for one tenant. Read through the PgError
// interface the driver exposes rather than importing pgconn (same helper as
// internal/apps/builder).
func isUniqueViolation(err error) bool {
	type pgErr interface {
		SQLState() string
	}
	var pe pgErr
	if errors.As(err, &pe) {
		return pe.SQLState() == "23505"
	}
	return false
}
