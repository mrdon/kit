package services

import (
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/crypto"
)

// Caller represents the authenticated user making a request.
// Both agent tools (Slack) and MCP tools construct a Caller from their auth context.
type Caller struct {
	TenantID uuid.UUID
	UserID   uuid.UUID
	Identity string      // slack_user_id (or MCP identity); used for Slack DM ops + audit attribution
	Roles    []string    // role names the user holds (display + permission checks by name)
	RoleIDs  []uuid.UUID // role IDs the user holds (used by scope-table joins)
	IsAdmin  bool
	// Timezone is the IANA tz of the caller, resolved as user.Timezone with
	// fallback to tenant.Timezone, then "UTC". Always populated.
	Timezone string
}

// Location returns the caller's *time.Location, falling back to UTC on parse failure.
func (c *Caller) Location() *time.Location {
	if c.Timezone == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// ResolveTimezone picks the caller-appropriate IANA timezone given user and
// tenant defaults. user wins, then tenant, then "UTC".
func ResolveTimezone(userTZ, tenantTZ string) string {
	if userTZ != "" {
		return userTZ
	}
	if tenantTZ != "" {
		return tenantTZ
	}
	return "UTC"
}

// ToolMeta defines a tool's metadata, shared between agent and MCP adapters.
//
// VisibleToRoles gates which roles can see the tool in their catalog. An empty
// slice means "visible to everyone" (subject to AdminOnly). A populated list
// restricts visibility to callers who hold at least one of the listed roles;
// this is the surface used by builder-exposed tools so a script published for
// bartenders doesn't leak into the manager tool catalog.
type ToolMeta struct {
	Name           string
	Description    string
	Schema         map[string]any // JSON Schema for input
	AdminOnly      bool
	VisibleToRoles []string
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
	Jobs     *JobService
	Tenants  *TenantService
	Users    *UserService
	Sessions *SessionService
	Enc      *crypto.Encryptor
}

// New creates a Services bundle with all service instances.
func New(pool *pgxpool.Pool, enc *crypto.Encryptor) *Services {
	return &Services{
		Skills:   &SkillService{pool: pool},
		Rules:    &RuleService{pool: pool},
		Memories: &MemoryService{pool: pool},
		Roles:    &RoleService{pool: pool},
		Jobs:     &JobService{pool: pool},
		Tenants:  &TenantService{pool: pool},
		Users:    &UserService{pool: pool},
		Sessions: &SessionService{pool: pool},
		Enc:      enc,
	}
}

// hasRole checks if the caller has a specific role.
func hasRole(c *Caller, role string) bool {
	return slices.Contains(c.Roles, role)
}
