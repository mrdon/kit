package services

import (
	"slices"

	"github.com/mrdon/kit/internal/models"
)

// CallerKind says what sort of thing is acting. Authorization is about
// what the actor may do; which human is accountable, if any, is a separate
// fact carried in UserID.
type CallerKind string

const (
	// CallerUser is a person: roles decide what they may do and they are
	// accountable for it themselves. The zero Kind also means user, so a
	// bare Caller{} literal in a test still behaves as a person.
	CallerUser CallerKind = "user"
	// CallerDevice is a paired shared device (the trivia laptop, the bar
	// iPad). It holds capabilities, not roles, and no human is accountable
	// per action.
	CallerDevice CallerKind = "device"
	// CallerAgent is a scheduled job running on behalf of its owner. It
	// never exceeds the owner (models.Policy plus the owner's live roles)
	// and the owner is the accountable human.
	CallerAgent CallerKind = "agent"
	// CallerWidget is the anonymous website visitor behind the chat widget.
	CallerWidget CallerKind = "widget"
)

// IsUser reports whether the caller is a person.
func (c *Caller) IsUser() bool {
	return c.Kind == CallerUser || c.Kind == ""
}

// HasCapability reports whether a non-user caller holds cap. Users never
// hold capabilities directly; their roles are mapped to the same checks by
// the route wrapper, so this is false for them by design.
func (c *Caller) HasCapability(cap string) bool {
	return slices.Contains(c.Capabilities, cap)
}

// NewUserCaller builds the caller for a signed-in person from their
// resolved roles. Every surface that authenticates a user (session cookie,
// MCP bearer, Slack event, scheduled job owner) goes through here.
func NewUserCaller(tenant *models.Tenant, user *models.User, cr CallerRoles) *Caller {
	return &Caller{
		Kind:     CallerUser,
		TenantID: tenant.ID,
		UserID:   user.ID,
		Identity: user.SlackUserID,
		Roles:    cr.Names,
		RoleIDs:  cr.IDs,
		IsAdmin:  slices.Contains(cr.Names, models.RoleAdmin),
		Timezone: ResolveTimezone(user.Timezone, tenant.Timezone),
	}
}

// NewWidgetCaller builds the anonymous caller for the website chat widget:
// member-level read access to knowledge, with Kit's own documentation and
// job-wired skills hidden.
func NewWidgetCaller(tenant *models.Tenant) *Caller {
	return &Caller{
		Kind:                    CallerWidget,
		TenantID:                tenant.ID,
		Label:                   "Website widget",
		Roles:                   []string{models.RoleMember},
		IsAdmin:                 false,
		HideBuiltinSkills:       true,
		HideJobReferencedSkills: true,
		Timezone:                ResolveTimezone("", tenant.Timezone),
	}
}

// NewDeviceCaller builds the caller for a paired device. It has no roles
// and no Identity; routes that need a person must refuse it.
func NewDeviceCaller(tenant *models.Tenant, actor *models.Actor) *Caller {
	return &Caller{
		Kind:         CallerDevice,
		TenantID:     tenant.ID,
		ActorID:      actor.ID,
		Label:        actor.Label,
		Capabilities: slices.Clone(actor.Capabilities),
		Timezone:     ResolveTimezone("", tenant.Timezone),
	}
}
