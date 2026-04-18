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
	ScopeType       ScopeType
	ScopeValue      string
	SourceSessionID *uuid.UUID
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func CreateMemory(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, content string, scopeType ScopeType, scopeValue string, sessionID uuid.UUID) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO memories (id, tenant_id, content, scope_type, scope_value, source_session_id)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, uuid.New(), tenantID, content, scopeType, scopeValue, sessionID)
	if err != nil {
		return fmt.Errorf("creating memory: %w", err)
	}
	return nil
}

func DeleteMemory(ctx context.Context, pool *pgxpool.Pool, tenantID, memoryID uuid.UUID) error {
	_, err := pool.Exec(ctx, `DELETE FROM memories WHERE tenant_id = $1 AND id = $2`, tenantID, memoryID)
	if err != nil {
		return fmt.Errorf("deleting memory: %w", err)
	}
	return nil
}

// SearchMemories searches memories visible to the user (user-scoped + tenant-scoped + role-scoped).
func SearchMemories(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, slackUserID string, userRoles []string, query string) ([]Memory, error) {
	scopeSQL, scopeArgs := ScopeFilter("", 2, slackUserID, userRoles)
	ftsParam := 2 + len(scopeArgs)
	args := append([]any{tenantID}, scopeArgs...)
	args = append(args, query)
	rows, err := pool.Query(ctx, fmt.Sprintf(`
		SELECT id, tenant_id, content, scope_type, scope_value, source_session_id, created_at, updated_at
		FROM memories
		WHERE tenant_id = $1
		AND (%s)
		AND to_tsvector('english', content) @@ plainto_tsquery('english', $%d)
		ORDER BY created_at DESC
		LIMIT 10
	`, scopeSQL, ftsParam), args...)
	if err != nil {
		return nil, fmt.Errorf("searching memories: %w", err)
	}
	defer rows.Close()

	var memories []Memory
	for rows.Next() {
		var m Memory
		if err := rows.Scan(&m.ID, &m.TenantID, &m.Content, &m.ScopeType, &m.ScopeValue,
			&m.SourceSessionID, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		memories = append(memories, m)
	}
	return memories, rows.Err()
}

// GetRecentMemories returns the N most recent memories visible to the user.
func GetRecentMemories(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, slackUserID string, userRoles []string, limit int) ([]Memory, error) {
	scopeSQL, scopeArgs := ScopeFilter("", 2, slackUserID, userRoles)
	limitParam := 2 + len(scopeArgs)
	args := append([]any{tenantID}, scopeArgs...)
	args = append(args, limit)
	rows, err := pool.Query(ctx, fmt.Sprintf(`
		SELECT id, tenant_id, content, scope_type, scope_value, source_session_id, created_at, updated_at
		FROM memories
		WHERE tenant_id = $1
		AND (%s)
		ORDER BY created_at DESC
		LIMIT $%d
	`, scopeSQL, limitParam), args...)
	if err != nil {
		return nil, fmt.Errorf("getting recent memories: %w", err)
	}
	defer rows.Close()

	var memories []Memory
	for rows.Next() {
		var m Memory
		if err := rows.Scan(&m.ID, &m.TenantID, &m.Content, &m.ScopeType, &m.ScopeValue,
			&m.SourceSessionID, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		memories = append(memories, m)
	}
	return memories, rows.Err()
}
