package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrMemoryNotFound is returned by GetMemoryByKey when no memory is stored
// under the given key within the caller's scope.
var ErrMemoryNotFound = errors.New("memory not found")

type Memory struct {
	ID              uuid.UUID
	TenantID        uuid.UUID
	Content         string
	Key             *string
	SourceSessionID *uuid.UUID
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// CreateMemory inserts a memory pointing at the canonical scope row for
// (tenantID, roleID|userID|nil). Both roleID and userID nil = tenant-wide.
// The session FK is nullable — callers outside the live-chat path (MCP
// server, builder scripts) have no enclosing session and pass uuid.Nil,
// which we translate to NULL here rather than letting the zero UUID hit
// the FK constraint.
//
// key is an optional dedup key. When nil, the memory is append-only (the
// classic "remember this fact" behavior — many rows accumulate). When set,
// the write upserts on (scope_id, key): the first save inserts, later saves
// with the same key overwrite the row's content in place. This makes a keyed
// memory a single mutable value, suitable for cursors/watermarks.
func CreateMemory(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, content string, key string, roleID, userID *uuid.UUID, sessionID uuid.UUID) error {
	var sessionArg any
	if sessionID == uuid.Nil {
		sessionArg = nil
	} else {
		sessionArg = sessionID
	}
	var keyArg any
	if key != "" {
		keyArg = key
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("beginning tx: %w", err)
	}
	defer tx.Rollback(ctx)

	scopeID, err := GetOrCreateScopeTx(ctx, tx, tenantID, roleID, userID)
	if err != nil {
		return fmt.Errorf("get-or-create scope: %w", err)
	}

	// Keyed writes upsert in place; keyless writes always insert. The
	// ON CONFLICT target names the partial unique index's predicate so
	// Postgres infers memories_scope_key_idx (keyless rows never conflict).
	if _, err := tx.Exec(ctx, `
		INSERT INTO memories (id, tenant_id, content, key, scope_id, source_session_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (scope_id, key) WHERE key IS NOT NULL
		DO UPDATE SET content = EXCLUDED.content,
		             source_session_id = EXCLUDED.source_session_id,
		             updated_at = now()
	`, uuid.New(), tenantID, content, keyArg, scopeID, sessionArg); err != nil {
		return fmt.Errorf("creating memory: %w", err)
	}
	return tx.Commit(ctx)
}

// GetMemoryByKey returns the single memory with the given dedup key visible to
// the user (user-scoped + tenant-scoped + role-scoped), or ErrMemoryNotFound if
// none. A key is unique per scope, so at most one row is returned per scope;
// when the same key exists in more than one visible scope, the most recently
// updated wins.
func GetMemoryByKey(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID, roleIDs []uuid.UUID, key string) (*Memory, error) {
	scopeSQL, scopeArgs := ScopeFilterIDs("sc", 2, userID, roleIDs)
	keyParam := 2 + len(scopeArgs)
	args := append([]any{tenantID}, scopeArgs...)
	args = append(args, key)
	rows, err := pool.Query(ctx, fmt.Sprintf(`
		SELECT m.id, m.tenant_id, m.content, m.key, m.source_session_id, m.created_at, m.updated_at
		FROM memories m
		JOIN scopes sc ON sc.id = m.scope_id
		WHERE m.tenant_id = $1
		AND (%s)
		AND m.key = $%d
		ORDER BY m.updated_at DESC
		LIMIT 1
	`, scopeSQL, keyParam), args...)
	if err != nil {
		return nil, fmt.Errorf("getting memory by key: %w", err)
	}
	defer rows.Close()
	found, err := scanMemories(rows)
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, ErrMemoryNotFound
	}
	return &found[0], nil
}

func DeleteMemory(ctx context.Context, pool *pgxpool.Pool, tenantID, memoryID uuid.UUID) error {
	_, err := pool.Exec(ctx, `DELETE FROM memories WHERE tenant_id = $1 AND id = $2`, tenantID, memoryID)
	if err != nil {
		return fmt.Errorf("deleting memory: %w", err)
	}
	return nil
}

// SearchMemories searches memories visible to the user (user-scoped + tenant-scoped + role-scoped).
func SearchMemories(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID, roleIDs []uuid.UUID, query string) ([]Memory, error) {
	scopeSQL, scopeArgs := ScopeFilterIDs("sc", 2, userID, roleIDs)
	ftsParam := 2 + len(scopeArgs)
	args := append([]any{tenantID}, scopeArgs...)
	args = append(args, query)
	rows, err := pool.Query(ctx, fmt.Sprintf(`
		SELECT m.id, m.tenant_id, m.content, m.key, m.source_session_id, m.created_at, m.updated_at
		FROM memories m
		JOIN scopes sc ON sc.id = m.scope_id
		WHERE m.tenant_id = $1
		AND (%s)
		AND to_tsvector('english', m.content) @@ plainto_tsquery('english', $%d)
		ORDER BY m.created_at DESC
		LIMIT 10
	`, scopeSQL, ftsParam), args...)
	if err != nil {
		return nil, fmt.Errorf("searching memories: %w", err)
	}
	defer rows.Close()
	return scanMemories(rows)
}

// GetRecentMemories returns the N most recent memories visible to the user.
func GetRecentMemories(ctx context.Context, pool *pgxpool.Pool, tenantID, userID uuid.UUID, roleIDs []uuid.UUID, limit int) ([]Memory, error) {
	scopeSQL, scopeArgs := ScopeFilterIDs("sc", 2, userID, roleIDs)
	limitParam := 2 + len(scopeArgs)
	args := append([]any{tenantID}, scopeArgs...)
	args = append(args, limit)
	rows, err := pool.Query(ctx, fmt.Sprintf(`
		SELECT m.id, m.tenant_id, m.content, m.key, m.source_session_id, m.created_at, m.updated_at
		FROM memories m
		JOIN scopes sc ON sc.id = m.scope_id
		WHERE m.tenant_id = $1
		AND (%s)
		ORDER BY m.created_at DESC
		LIMIT $%d
	`, scopeSQL, limitParam), args...)
	if err != nil {
		return nil, fmt.Errorf("getting recent memories: %w", err)
	}
	defer rows.Close()
	return scanMemories(rows)
}

func scanMemories(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]Memory, error) {
	var memories []Memory
	for rows.Next() {
		var m Memory
		if err := rows.Scan(&m.ID, &m.TenantID, &m.Content, &m.Key, &m.SourceSessionID, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning memory: %w", err)
		}
		memories = append(memories, m)
	}
	return memories, rows.Err()
}
