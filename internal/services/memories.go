package services

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
)

// MemoryTools defines the shared tool metadata for memory operations.
var MemoryTools = []ToolMeta{
	{Name: "save_memory", Description: "Save a fact for future conversations.", Schema: propsReq(map[string]any{
		"content": field("string", "The fact to remember"),
		"scope":   field("string", "Scope: 'user' (default), 'tenant', or a role name"),
	}, "content")},
	{Name: "search_memories", Description: "Search saved memories for relevant facts.", Schema: propsReq(map[string]any{"query": field("string", "Search query")}, "query")},
	{Name: "forget_memory", Description: "Delete a specific memory.", Schema: propsReq(map[string]any{"memory_id": field("string", "The memory UUID")}, "memory_id"), AdminOnly: true},
}

// MemoryService handles memory operations with authorization.
type MemoryService struct {
	pool *pgxpool.Pool
}

// Save creates a memory with scope resolution.
// scope: "user" (default), "tenant", or a role name.
func (s *MemoryService) Save(ctx context.Context, c *Caller, content, scope string, sessionID uuid.UUID) error {
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
	return models.CreateMemory(ctx, s.pool, c.TenantID, content, roleID, userID, sessionID)
}

// Search searches memories visible to the caller.
func (s *MemoryService) Search(ctx context.Context, c *Caller, query string) ([]models.Memory, error) {
	return models.SearchMemories(ctx, s.pool, c.TenantID, c.UserID, c.RoleIDs, query)
}

// Forget deletes a memory. Admins can delete any; non-admins only their own.
func (s *MemoryService) Forget(ctx context.Context, c *Caller, memoryID uuid.UUID) error {
	if c.IsAdmin {
		return models.DeleteMemory(ctx, s.pool, c.TenantID, memoryID)
	}
	return ErrForbidden
}
