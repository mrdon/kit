package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mrdon/kit/internal/services"
)

func registerRoleTools(r *Registry, isAdmin bool) {
	for _, meta := range services.RoleTools {
		if meta.AdminOnly && !isAdmin {
			continue
		}
		r.Register(Def{
			Name:        meta.Name,
			Description: meta.Description,
			Schema:      meta.Schema,
			AdminOnly:   meta.AdminOnly,
			Handler:     roleHandler(meta.Name),
		})
	}
}

func roleHandler(name string) HandlerFunc {
	switch name {
	case "list_roles":
		return handleListRoles
	case "list_role_members":
		return handleListRoleMembers
	case "create_role":
		return handleCreateRole
	case "assign_role":
		return handleAssignRole
	case "unassign_role":
		return handleUnassignRole
	case "update_role":
		return handleUpdateRole
	case "delete_role":
		return handleDeleteRole
	default:
		return func(_ *ExecContext, _ json.RawMessage) (string, error) {
			return "", fmt.Errorf("unknown role tool: %s", name)
		}
	}
}

func handleListRoles(ec *ExecContext, _ json.RawMessage) (string, error) {
	roles, err := ec.Svc.Roles.List(ec.Ctx, ec.Caller())
	if err != nil {
		return "", err
	}
	if len(roles) == 0 {
		return "No roles defined yet.", nil
	}
	var b strings.Builder
	b.WriteString("Roles:\n")
	for _, role := range roles {
		desc := ""
		if role.Description != nil {
			desc = " — " + *role.Description
		}
		fmt.Fprintf(&b, "- %s%s\n", role.Name, desc)
	}
	return b.String(), nil
}

func handleListRoleMembers(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		RoleName string `json:"role_name"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	members, err := ec.Svc.Roles.ListMembers(ec.Ctx, ec.Caller(), inp.RoleName)
	if err != nil {
		return "", err
	}
	if len(members) == 0 {
		return "No users assigned to role '" + inp.RoleName + "'.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Members of '%s':\n", inp.RoleName)
	for _, m := range members {
		name := m.SlackUserID
		if m.DisplayName != nil {
			name = *m.DisplayName + " (" + m.SlackUserID + ")"
		}
		b.WriteString("- " + name + "\n")
	}
	return b.String(), nil
}

func handleCreateRole(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct{ Name, Description string }
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	role, err := ec.Svc.Roles.Create(ec.Ctx, ec.Caller(), inp.Name, inp.Description)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Role '%s' created.", role.Name), nil
}

func handleAssignRole(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		SlackUserID string `json:"slack_user_id"`
		RoleName    string `json:"role_name"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	if err := ec.Svc.Roles.Assign(ec.Ctx, ec.Caller(), inp.SlackUserID, inp.RoleName); err != nil {
		return "", err
	}
	return fmt.Sprintf("Role '%s' assigned to %s.", inp.RoleName, inp.SlackUserID), nil
}

func handleUnassignRole(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct {
		SlackUserID string `json:"slack_user_id"`
		RoleName    string `json:"role_name"`
	}
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	err := ec.Svc.Roles.Unassign(ec.Ctx, ec.Caller(), inp.SlackUserID, inp.RoleName)
	if errors.Is(err, services.ErrNotFound) {
		return "User not found.", nil
	}
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Role '%s' removed from %s.", inp.RoleName, inp.SlackUserID), nil
}

func handleUpdateRole(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct{ Name, Description string }
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	if err := ec.Svc.Roles.Update(ec.Ctx, ec.Caller(), inp.Name, inp.Description); err != nil {
		return "", err
	}
	return fmt.Sprintf("Role '%s' updated.", inp.Name), nil
}

func handleDeleteRole(ec *ExecContext, input json.RawMessage) (string, error) {
	var inp struct{ Name string }
	if err := json.Unmarshal(input, &inp); err != nil {
		return "", err
	}
	if err := ec.Svc.Roles.Delete(ec.Ctx, ec.Caller(), inp.Name); err != nil {
		return "", err
	}
	return fmt.Sprintf("Role '%s' deleted.", inp.Name), nil
}
