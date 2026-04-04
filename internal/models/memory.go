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
	ScopeType       string
	ScopeValue      string
	SourceSessionID *uuid.UUID
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func CreateMemory(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, content, scopeType, scopeValue string, sessionID uuid.UUID) error {
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
	rows, err := pool.Query(ctx, `
		SELECT id, tenant_id, content, scope_type, scope_value, source_session_id, created_at, updated_at
		FROM memories
		WHERE tenant_id = $1
		AND (
			(scope_type = 'user' AND scope_value = $2)
			OR (scope_type = 'tenant' AND scope_value = '*')
			OR (scope_type = 'role' AND scope_value = ANY($3))
		)
		AND to_tsvector('english', content) @@ plainto_tsquery('english', $4)
		ORDER BY created_at DESC
		LIMIT 10
	`, tenantID, slackUserID, userRoles, query)
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
	rows, err := pool.Query(ctx, `
		SELECT id, tenant_id, content, scope_type, scope_value, source_session_id, created_at, updated_at
		FROM memories
		WHERE tenant_id = $1
		AND (
			(scope_type = 'user' AND scope_value = $2)
			OR (scope_type = 'tenant' AND scope_value = '*')
			OR (scope_type = 'role' AND scope_value = ANY($3))
		)
		ORDER BY created_at DESC
		LIMIT $4
	`, tenantID, slackUserID, userRoles, limit)
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
