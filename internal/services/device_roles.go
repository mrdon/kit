package services

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
)

// Device roles. A paired device authorises its console pages by capability,
// and holds roles for what Kit knowledge it can see. Two rules differ from
// people: a device holds only the roles assigned to it (no member
// catchall), and it can never be an admin — anyone standing at the device
// would hold the admin's rights.

// ErrDeviceAdmin refuses giving a device the admin role.
var ErrDeviceAdmin = errors.New("a device can't be an admin")

// ErrDeviceAmbiguous is returned when a device reference matches more than
// one paired device by label.
var ErrDeviceAmbiguous = errors.New("more than one paired device has that name; use its id")

// ResolveDeviceRoles returns the roles a device holds, for building its
// Caller. It is the device counterpart of ResolveCallerRoles.
func (s *RoleService) ResolveDeviceRoles(ctx context.Context, tenantID, actorID uuid.UUID) (CallerRoles, error) {
	rows, err := models.ListActorRoles(ctx, s.pool, tenantID, &actorID)
	if err != nil {
		return CallerRoles{}, err
	}
	var cr CallerRoles
	for _, r := range rows {
		cr.Names = append(cr.Names, r.RoleName)
		cr.IDs = append(cr.IDs, r.RoleID)
	}
	return cr, nil
}

// FindDevice resolves a device reference — its id, or its label ignoring
// case — to a live paired device. ErrNotFound when nothing matches.
func (s *RoleService) FindDevice(ctx context.Context, c *Caller, ref string) (*models.Actor, error) {
	if !c.IsAdmin {
		return nil, ErrForbidden
	}
	ref = strings.TrimSpace(ref)
	devices, err := models.ListActors(ctx, s.pool, c.TenantID, models.ActorKindDevice)
	if err != nil {
		return nil, fmt.Errorf("listing devices: %w", err)
	}
	var match *models.Actor
	for i := range devices {
		d := &devices[i]
		if d.RevokedAt != nil {
			continue
		}
		if d.ID.String() == ref {
			return d, nil
		}
		if strings.EqualFold(d.Label, ref) {
			if match != nil {
				return nil, ErrDeviceAmbiguous
			}
			match = d
		}
	}
	if match == nil {
		return nil, ErrNotFound
	}
	return match, nil
}

// AssignDevice gives a device a role. Admin only; refuses `admin`.
func (s *RoleService) AssignDevice(ctx context.Context, c *Caller, deviceRef, roleName string) (*models.Actor, error) {
	return s.changeDeviceRole(ctx, c, deviceRef, roleName, models.AssignActorRole)
}

// UnassignDevice removes a role from a device. Admin only.
func (s *RoleService) UnassignDevice(ctx context.Context, c *Caller, deviceRef, roleName string) (*models.Actor, error) {
	return s.changeDeviceRole(ctx, c, deviceRef, roleName, models.UnassignActorRole)
}

type actorRoleWrite func(context.Context, *pgxpool.Pool, uuid.UUID, uuid.UUID, string) error

func (s *RoleService) changeDeviceRole(ctx context.Context, c *Caller, deviceRef, roleName string, write actorRoleWrite) (*models.Actor, error) {
	if !c.IsAdmin {
		return nil, ErrForbidden
	}
	if roleName == models.RoleAdmin {
		return nil, ErrDeviceAdmin
	}
	exists, err := models.RoleExists(ctx, s.pool, c.TenantID, roleName)
	if err != nil {
		return nil, fmt.Errorf("checking role: %w", err)
	}
	if !exists {
		return nil, fmt.Errorf("%q: %w", roleName, ErrRoleNotFound)
	}
	device, err := s.FindDevice(ctx, c, deviceRef)
	if err != nil {
		return nil, err
	}
	if err := write(ctx, s.pool, c.TenantID, device.ID, roleName); err != nil {
		return nil, err
	}
	return device, nil
}

// ListDeviceMembers lists the live devices holding a role. Admin only.
func (s *RoleService) ListDeviceMembers(ctx context.Context, c *Caller, roleName string) ([]models.Actor, error) {
	if !c.IsAdmin {
		return nil, ErrForbidden
	}
	return models.ListRoleActors(ctx, s.pool, c.TenantID, roleName)
}

// DeviceRoles is one paired device and the roles it holds, for the roles
// matrix.
type DeviceRoles struct {
	ActorID string
	Label   string
	Roles   []string
}

// deviceMembership lists every live paired device with its roles, and adds
// each to counts so role columns count devices alongside people.
func (s *RoleService) deviceMembership(ctx context.Context, tenantID uuid.UUID, counts map[string]int) ([]DeviceRoles, error) {
	devices, err := models.ListActors(ctx, s.pool, tenantID, models.ActorKindDevice)
	if err != nil {
		return nil, fmt.Errorf("listing devices: %w", err)
	}
	held, err := models.ListActorRoles(ctx, s.pool, tenantID, nil)
	if err != nil {
		return nil, err
	}
	byActor := make(map[uuid.UUID][]string)
	for _, r := range held {
		byActor[r.ActorID] = append(byActor[r.ActorID], r.RoleName)
	}
	out := make([]DeviceRoles, 0, len(devices))
	for _, d := range devices {
		if d.RevokedAt != nil {
			continue
		}
		roles := byActor[d.ID]
		if roles == nil {
			roles = []string{}
		}
		for _, n := range roles {
			counts[n]++
		}
		out = append(out, DeviceRoles{ActorID: d.ID.String(), Label: d.Label, Roles: roles})
	}
	return out, nil
}

// FormatDeviceLine renders a device for role tool output, shared by the
// agent and MCP surfaces.
func FormatDeviceLine(a *models.Actor) string {
	return fmt.Sprintf("%s — device id:%s", a.Label, a.ID)
}
