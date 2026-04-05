package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mrdon/kit/internal/models"
)

func registerRoleTools(r *Registry, isAdmin bool) {
	if !isAdmin {
		return
	}

	r.Register(Def{
		Name: "list_roles", Description: "List all roles.",
		Schema: props(map[string]any{}), AdminOnly: true,
		Handler: func(ec *ExecContext, _ json.RawMessage) (string, error) {
			roles, err := models.ListRoles(ec.Ctx, ec.Pool, ec.Tenant.ID)
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
		},
	})

	r.Register(Def{
		Name: "create_role", Description: "Create a new role.",
		Schema: propsReq(map[string]any{
			"name":        field("string", "Role name (e.g., 'bartender')"),
			"description": field("string", "Brief description"),
		}, "name"), AdminOnly: true,
		Handler: func(ec *ExecContext, input json.RawMessage) (string, error) {
			var inp struct{ Name, Description string }
			if err := json.Unmarshal(input, &inp); err != nil {
				return "", err
			}
			role, err := models.CreateRole(ec.Ctx, ec.Pool, ec.Tenant.ID, inp.Name, inp.Description)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Role '%s' created.", role.Name), nil
		},
	})

	r.Register(Def{
		Name: "assign_role", Description: "Assign a role to a Slack user.",
		Schema: propsReq(map[string]any{
			"slack_user_id": field("string", "Slack user ID (e.g., 'U1234567890')"),
			"role_name":     field("string", "Role name to assign"),
		}, "slack_user_id", "role_name"), AdminOnly: true,
		Handler: func(ec *ExecContext, input json.RawMessage) (string, error) {
			var inp struct {
				SlackUserID string `json:"slack_user_id"`
				RoleName    string `json:"role_name"`
			}
			if err := json.Unmarshal(input, &inp); err != nil {
				return "", err
			}
			user, err := models.GetOrCreateUser(ec.Ctx, ec.Pool, ec.Tenant.ID, inp.SlackUserID, "", false)
			if err != nil {
				return "", err
			}
			if err := models.AssignRole(ec.Ctx, ec.Pool, ec.Tenant.ID, user.ID, inp.RoleName); err != nil {
				return "", err
			}
			return fmt.Sprintf("Role '%s' assigned to %s.", inp.RoleName, inp.SlackUserID), nil
		},
	})

	r.Register(Def{
		Name: "unassign_role", Description: "Remove a role from a user.",
		Schema: propsReq(map[string]any{
			"slack_user_id": field("string", "Slack user ID"),
			"role_name":     field("string", "Role name to remove"),
		}, "slack_user_id", "role_name"), AdminOnly: true,
		Handler: func(ec *ExecContext, input json.RawMessage) (string, error) {
			var inp struct {
				SlackUserID string `json:"slack_user_id"`
				RoleName    string `json:"role_name"`
			}
			if err := json.Unmarshal(input, &inp); err != nil {
				return "", err
			}
			user, err := models.GetUserBySlackID(ec.Ctx, ec.Pool, ec.Tenant.ID, inp.SlackUserID)
			if err != nil {
				return "", err
			}
			if user == nil {
				return "User not found.", nil
			}
			if err := models.UnassignRole(ec.Ctx, ec.Pool, ec.Tenant.ID, user.ID, inp.RoleName); err != nil {
				return "", err
			}
			return fmt.Sprintf("Role '%s' removed from %s.", inp.RoleName, inp.SlackUserID), nil
		},
	})

	r.Register(Def{
		Name: "update_role", Description: "Update a role's description.",
		Schema: propsReq(map[string]any{
			"name":        field("string", "Role name"),
			"description": field("string", "New description"),
		}, "name", "description"), AdminOnly: true,
		Handler: func(ec *ExecContext, input json.RawMessage) (string, error) {
			var inp struct{ Name, Description string }
			if err := json.Unmarshal(input, &inp); err != nil {
				return "", err
			}
			if err := models.UpdateRole(ec.Ctx, ec.Pool, ec.Tenant.ID, inp.Name, inp.Description); err != nil {
				return "", err
			}
			return fmt.Sprintf("Role '%s' updated.", inp.Name), nil
		},
	})

	r.Register(Def{
		Name: "delete_role", Description: "Delete a role.",
		Schema: propsReq(map[string]any{"name": field("string", "Role name")}, "name"), AdminOnly: true,
		Handler: func(ec *ExecContext, input json.RawMessage) (string, error) {
			var inp struct{ Name string }
			if err := json.Unmarshal(input, &inp); err != nil {
				return "", err
			}
			if err := models.DeleteRole(ec.Ctx, ec.Pool, ec.Tenant.ID, inp.Name); err != nil {
				return "", err
			}
			return fmt.Sprintf("Role '%s' deleted.", inp.Name), nil
		},
	})
}
