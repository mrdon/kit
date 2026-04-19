package models

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Memory struct {
	ID              uuid.UUID
	TenantID        uuid.UUID
	Content         string
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
func CreateMemory(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, content string, roleID, userID *uuid.UUID, sessionID uuid.UUID) error {
	var sessionArg any
	if sessionID == uuid.Nil {
		sessionArg = nil
	} else {
		sessionArg = sessionID
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

	if _, err := tx.Exec(ctx, `
		INSERT INTO memories (id, tenant_id, content, scope_id, source_session_id)
		VALUES ($1, $2, $3, $4, $5)
	`, uuid.New(), tenantID, content, scopeID, sessionArg); err != nil {
		return fmt.Errorf("creating memory: %w", err)
	}
	return tx.Commit(ctx)
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
		SELECT m.id, m.tenant_id, m.content, m.source_session_id, m.created_at, m.updated_at
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
		SELECT m.id, m.tenant_id, m.content, m.source_session_id, m.created_at, m.updated_at
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
		if err := rows.Scan(&m.ID, &m.TenantID, &m.Content, &m.SourceSessionID, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning memory: %w", err)
		}
		memories = append(memories, m)
	}
	return memories, rows.Err()
}
