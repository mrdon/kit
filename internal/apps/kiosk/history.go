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

// HistoryDepth is how many previous URLs a board keeps.
//
// Three, because this is an undo for a bad paste, not an audit trail. In
// practice a rollback reaches for the last entry, occasionally the one before
// it; anything older is a URL nobody remembers the meaning of. Keeping the
// list short is also what makes it readable in the console without a
// disclosure control.
const HistoryDepth = 3

// URLChange is one previous target, newest first.
type URLChange struct {
	ID         uuid.UUID
	URL        string
	ReplacedAt time.Time
}

// ListURLHistory returns a board's previous URLs, newest first.
func ListURLHistory(ctx context.Context, pool *pgxpool.Pool, tenantID, boardID uuid.UUID) ([]URLChange, error) {
	const q = `SELECT id, url, replaced_at
	           FROM app_kiosk_board_urls
	           WHERE tenant_id = $1 AND board_id = $2
	           ORDER BY replaced_at DESC, id DESC
	           LIMIT $3`
	rows, err := pool.Query(ctx, q, tenantID, boardID, HistoryDepth)
	if err != nil {
		return nil, fmt.Errorf("querying kiosk url history: %w", err)
	}
	defer rows.Close()
	var out []URLChange
	for rows.Next() {
		var c URLChange
		if err := rows.Scan(&c.ID, &c.URL, &c.ReplacedAt); err != nil {
			return nil, fmt.Errorf("scanning kiosk url history: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating kiosk url history: %w", err)
	}
	return out, nil
}

// recordURLChange appends the URL a board is moving away from, then trims the
// list back to HistoryDepth.
//
// Called inside the update path with the value read before the write. It is
// best-effort by contract: losing a history row must never fail the repoint
// the admin actually asked for, so callers log and carry on. That is the right
// trade because the history is a convenience and the repoint is the job.
func recordURLChange(ctx context.Context, pool *pgxpool.Pool, tenantID, boardID uuid.UUID, previous string) error {
	if previous == "" {
		return nil // nothing was displaced; a board's first URL is not a change
	}
	const insert = `INSERT INTO app_kiosk_board_urls (tenant_id, board_id, url) VALUES ($1, $2, $3)`
	if _, err := pool.Exec(ctx, insert, tenantID, boardID, previous); err != nil {
		return fmt.Errorf("recording kiosk url change: %w", err)
	}
	return trimURLHistory(ctx, pool, tenantID, boardID)
}

// trimURLHistory deletes everything past HistoryDepth for one board.
func trimURLHistory(ctx context.Context, pool *pgxpool.Pool, tenantID, boardID uuid.UUID) error {
	const q = `DELETE FROM app_kiosk_board_urls
	           WHERE tenant_id = $1 AND board_id = $2 AND id NOT IN (
	               SELECT id FROM app_kiosk_board_urls
	               WHERE tenant_id = $1 AND board_id = $2
	               ORDER BY replaced_at DESC, id DESC
	               LIMIT $3
	           )`
	if _, err := pool.Exec(ctx, q, tenantID, boardID, HistoryDepth); err != nil {
		return fmt.Errorf("trimming kiosk url history: %w", err)
	}
	return nil
}

// currentURL reads just the URL column for a board, used to capture what an
// update is about to displace. Returns ErrNotFound for an unknown board so
// the update path can 404 before it writes anything.
func currentURL(ctx context.Context, pool *pgxpool.Pool, tenantID, boardID uuid.UUID) (string, error) {
	const q = `SELECT COALESCE(url, '') FROM app_kiosk_boards WHERE tenant_id = $1 AND id = $2`
	var url string
	err := pool.QueryRow(ctx, q, tenantID, boardID).Scan(&url)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("reading current kiosk board url: %w", err)
	}
	return url, nil
}

// ListURLHistoryByBoard returns every board's history for a tenant in one
// query, keyed by board id. The listing endpoint needs history for all boards
// at once, and the write path already caps each board at HistoryDepth rows, so
// the whole tenant's history is at most a few rows per screen — cheap enough
// to fetch flat rather than issue one query per board.
func ListURLHistoryByBoard(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (map[uuid.UUID][]URLChange, error) {
	const q = `SELECT board_id, id, url, replaced_at
	           FROM app_kiosk_board_urls
	           WHERE tenant_id = $1
	           ORDER BY board_id, replaced_at DESC, id DESC`
	rows, err := pool.Query(ctx, q, tenantID)
	if err != nil {
		return nil, fmt.Errorf("querying kiosk url history by board: %w", err)
	}
	defer rows.Close()
	out := make(map[uuid.UUID][]URLChange)
	for rows.Next() {
		var boardID uuid.UUID
		var c URLChange
		if err := rows.Scan(&boardID, &c.ID, &c.URL, &c.ReplacedAt); err != nil {
			return nil, fmt.Errorf("scanning kiosk url history: %w", err)
		}
		if len(out[boardID]) < HistoryDepth {
			out[boardID] = append(out[boardID], c)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating kiosk url history: %w", err)
	}
	return out, nil
}
