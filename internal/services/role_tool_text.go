package services

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mrdon/kit/internal/models"
)

// Shared bodies for the assign_role / unassign_role / list_role_members
// tools, so the agent and MCP handlers only parse input and wrap output.

// ErrRoleTarget is returned when a role change names neither or both of a
// person and a device.
var ErrRoleTarget = errors.New("pass exactly one of slack_user_id or device")

// ChangeRole assigns (assign=true) or removes a role from a person (by
// Slack id) or a paired device (by id or label), and returns the text to
// show. A missing person or device comes back as text, not an error, to
// match the existing unassign behaviour.
func (s *RoleService) ChangeRole(ctx context.Context, c *Caller, slackUserID, device, roleName string, assign bool) (string, error) {
	slackUserID, device = strings.TrimSpace(slackUserID), strings.TrimSpace(device)
	if (slackUserID == "") == (device == "") {
		return "", ErrRoleTarget
	}
	if device != "" {
		var d *models.Actor
		var err error
		if assign {
			d, err = s.AssignDevice(ctx, c, device, roleName)
		} else {
			d, err = s.UnassignDevice(ctx, c, device, roleName)
		}
		if errors.Is(err, ErrNotFound) {
			return "No paired device named " + device + ".", nil
		}
		if err != nil {
			return "", err
		}
		return roleChangeText(roleName, "device "+d.Label, assign), nil
	}
	var err error
	if assign {
		err = s.Assign(ctx, c, slackUserID, roleName)
	} else {
		err = s.Unassign(ctx, c, slackUserID, roleName)
	}
	if errors.Is(err, ErrNotFound) {
		return "User not found.", nil
	}
	if err != nil {
		return "", err
	}
	return roleChangeText(roleName, slackUserID, assign), nil
}

func roleChangeText(roleName, who string, assign bool) string {
	if assign {
		return fmt.Sprintf("Role '%s' assigned to %s.", roleName, who)
	}
	return fmt.Sprintf("Role '%s' removed from %s.", roleName, who)
}

// RoleMembersText lists the people and paired devices holding a role.
func (s *RoleService) RoleMembersText(ctx context.Context, c *Caller, roleName string) (string, error) {
	users, err := s.ListMembers(ctx, c, roleName)
	if err != nil {
		return "", err
	}
	devices, err := s.ListDeviceMembers(ctx, c, roleName)
	if err != nil {
		return "", err
	}
	if len(users) == 0 && len(devices) == 0 {
		return "No users or devices assigned to role '" + roleName + "'.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Members of '%s':\n", roleName)
	for i := range users {
		b.WriteString("- " + FormatUserLine(&users[i]) + "\n")
	}
	for i := range devices {
		b.WriteString("- " + FormatDeviceLine(&devices[i]) + "\n")
	}
	return b.String(), nil
}
