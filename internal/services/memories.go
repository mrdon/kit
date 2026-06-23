package services

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
)

// MemoryTools defines the shared tool metadata for memory operations.
var MemoryTools = []ToolMeta{
	{Name: "save_memory", Description: "Save a fact for future conversations. Pass an optional `key` to make it a single mutable value (re-saving with the same key+scope overwrites in place) — use this for cursors/watermarks instead of accumulating duplicate facts.", Schema: propsReq(map[string]any{
		"content": field("string", "The fact to remember"),
		"scope":   field("string", "Scope: 'user' (default), 'tenant', or a role name"),
		"key":     field("string", "Optional dedup key. When set, re-saving with the same key and scope overwrites the existing memory in place instead of appending a new one. Read it back with get_memory."),
	}, "content")},
	{Name: "search_memories", Description: "Search saved memories for relevant facts.", Schema: propsReq(map[string]any{"query": field("string", "Search query")}, "query")},
	{Name: "get_memory", Description: "Fetch a single memory by its exact dedup key (the `key` passed to save_memory). Returns the current value, or nothing if unset. Use for deterministic cursor/watermark lookups rather than fuzzy search_memories.", Schema: propsReq(map[string]any{"key": field("string", "The exact dedup key the memory was saved under")}, "key")},
	{Name: "forget_memory", Description: "Delete a specific memory.", Schema: propsReq(map[string]any{"memory_id": field("string", "The memory UUID")}, "memory_id"), AdminOnly: true},
}

// MemoryService handles memory operations with authorization.
type MemoryService struct {
	pool *pgxpool.Pool
}

// Save creates a memory with scope resolution.
// scope: "user" (default), "tenant", or a role name.
// key: optional dedup key. Empty = append (classic fact). Non-empty = upsert
// in place on (scope, key).
func (s *MemoryService) Save(ctx context.Context, c *Caller, content, scope, key string, sessionID uuid.UUID) error {
	if scope == "" {
		scope = string(models.ScopeTypeUser)
	}
	var roleID, userID *uuid.UUID
	switch scope {
	case string(models.ScopeTypeUser):
		userID = &c.UserID
	case string(models.ScopeTypeTenant):
		// both nil → tenant-wide
	default:
		if !c.IsAdmin && !hasRole(c, scope) {
			return ErrForbidden
		}
		rid, err := ResolveRoleID(ctx, s.pool, c.TenantID, scope)
		if err != nil {
			return err
		}
		roleID = &rid
	}
	return models.CreateMemory(ctx, s.pool, c.TenantID, content, key, roleID, userID, sessionID)
}

// Search searches memories visible to the caller.
func (s *MemoryService) Search(ctx context.Context, c *Caller, query string) ([]models.Memory, error) {
	return models.SearchMemories(ctx, s.pool, c.TenantID, c.UserID, c.RoleIDs, query)
}

// GetByKey fetches a single memory by its exact dedup key, within the caller's
// scope visibility. Returns nil if no memory is stored under that key.
func (s *MemoryService) GetByKey(ctx context.Context, c *Caller, key string) (*models.Memory, error) {
	return models.GetMemoryByKey(ctx, s.pool, c.TenantID, c.UserID, c.RoleIDs, key)
}

// Forget deletes a memory. Admins can delete any; non-admins only their own.
func (s *MemoryService) Forget(ctx context.Context, c *Caller, memoryID uuid.UUID) error {
	if c.IsAdmin {
		return models.DeleteMemory(ctx, s.pool, c.TenantID, memoryID)
	}
	return ErrForbidden
}
