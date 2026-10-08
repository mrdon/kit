package services

import (
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/crypto"
)

// Caller is the actor behind a request or an agent turn: a person, a
// paired device, a scheduled job, or the website widget. Build one with
// the constructors in caller.go; every surface (session cookie, MCP
// bearer, Slack event, scheduler) goes through them.
type Caller struct {
	// Kind says what is acting. The zero value means user.
	Kind CallerKind
	// ActorID is the actors row for a non-user; uuid.Nil for a person.
	ActorID uuid.UUID
	// Label names a non-user actor for audit ("Bar iPad"). Empty for users,
	// whose display name lives on the users row.
	Label string
	// Capabilities is what a non-user actor may do. Users carry none: their
	// roles are mapped onto the same checks by the route wrapper.
	Capabilities []string

	TenantID uuid.UUID
	// UserID is the accountable human, if any: the user themself, a job's
	// owner. uuid.Nil for a device or the widget.
	UserID   uuid.UUID
	Identity string      // slack_user_id (or MCP identity); used for Slack DM ops + audit attribution. Empty for non-users.
	Roles    []string    // role names the user holds (display + permission checks by name)
	RoleIDs  []uuid.UUID // role IDs the user holds (used by scope-table joins)
	IsAdmin  bool
	// Timezone is the IANA tz of the caller, resolved as user.Timezone with
	// fallback to tenant.Timezone, then "UTC". Always populated.
	Timezone string
	// HideBuiltinSkills suppresses Kit's built-in skills (user guide, etc.)
	// from skill listings and lookups. The website widget sets this so the
	// public Q&A bot can't surface Kit-product documentation when asked
	// about the tenant's knowledge base.
	HideBuiltinSkills bool
	// HideJobReferencedSkills excludes tenant skills wired as the
	// SkillID of any scheduled job. The widget sets this so workflow
	// skills (which the tenant uses internally to drive automation)
	// don't end up cited in answers to website visitors.
	HideJobReferencedSkills bool
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
	Skills          *SkillService
	Rules           *RuleService
	Memories        *MemoryService
	Roles           *RoleService
	Jobs            *JobService
	Tenants         *TenantService
	Users           *UserService
	Sessions        *SessionService
	WidgetTokens    *WidgetTokenService
	WidgetAnalytics *WidgetAnalyticsService
	Enc             *crypto.Encryptor
}

// New creates a Services bundle with all service instances. baseURL is
// used by the widget-token service to render the embed snippet; pass
// the public base URL of the Kit deployment. teamInfo is optional —
// pass nil to disable Slack team.info-backed features like
// TenantService.EnsureSlackDomain.
func New(pool *pgxpool.Pool, enc *crypto.Encryptor, baseURL string, teamInfo SlackTeamInfoFetcher) *Services {
	return &Services{
		Skills:          NewSkillService(pool),
		Rules:           &RuleService{pool: pool},
		Memories:        &MemoryService{pool: pool},
		Roles:           NewRoleService(pool),
		Jobs:            NewJobService(pool),
		Tenants:         &TenantService{pool: pool, enc: enc, teamInfo: teamInfo},
		Users:           &UserService{pool: pool},
		Sessions:        &SessionService{pool: pool},
		WidgetTokens:    NewWidgetTokenService(pool, baseURL),
		WidgetAnalytics: NewWidgetAnalyticsService(pool),
		Enc:             enc,
	}
}

// hasRole checks if the caller has a specific role.
func hasRole(c *Caller, role string) bool {
	return slices.Contains(c.Roles, role)
}
