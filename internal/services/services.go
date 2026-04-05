package services

import (
	"errors"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Caller represents the authenticated user making a request.
// Both agent tools (Slack) and MCP tools construct a Caller from their auth context.
type Caller struct {
	TenantID uuid.UUID
	UserID   uuid.UUID
	Identity string   // scope_value for user-scoped items (slack_user_id or MCP identity)
	Roles    []string // role names the user holds
	IsAdmin  bool
}

// ToolMeta defines a tool's metadata, shared between agent and MCP adapters.
type ToolMeta struct {
	Name        string
	Description string
	Schema      map[string]any // JSON Schema for input
	AdminOnly   bool
}

// Common service errors.
var (
	ErrNotFound  = errors.New("not found")
	ErrForbidden = errors.New("forbidden")
)

// Services bundles all service instances for convenient passing to tool adapters.
type Services struct {
	Skills   *SkillService
	Rules    *RuleService
	Memories *MemoryService
	Roles    *RoleService
	Tasks    *TaskService
	Tenants  *TenantService
}

// New creates a Services bundle with all service instances.
func New(pool *pgxpool.Pool) *Services {
	return &Services{
		Skills:   &SkillService{pool: pool},
		Rules:    &RuleService{pool: pool},
		Memories: &MemoryService{pool: pool},
		Roles:    &RoleService{pool: pool},
		Tasks:    &TaskService{pool: pool},
		Tenants:  &TenantService{pool: pool},
	}
}

// hasRole checks if the caller has a specific role.
func hasRole(c *Caller, role string) bool {
	return slices.Contains(c.Roles, role)
}
