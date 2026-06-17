package console

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"

	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
)

// The roles page is the admin surface for "who is in which role". It's a
// matrix: every role down one axis, every workspace user down the other,
// with assign/unassign toggling membership. All three endpoints are
// admin-gated (AdminJSON) and reuse services.RoleService so the wording and
// rules match the MCP role tools — the CLAUDE.md tool-parity rule.

type roleInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	MemberCount int    `json:"member_count"`
}

type roleUserInfo struct {
	UserID      string   `json:"user_id"`
	SlackUserID string   `json:"slack_user_id"`
	DisplayName string   `json:"display_name"`
	Roles       []string `json:"roles"`
}

type rolesResponse struct {
	Roles []roleInfo     `json:"roles"`
	Users []roleUserInfo `json:"users"`
}

// handleRoles returns the full role/user matrix: every role (with a member
// count) and every user (with the roles they hold).
func (a *App) handleRoles(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	if caller == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	svc := services.NewRoleService(a.pool)

	roles, err := svc.List(r.Context(), caller)
	if err != nil {
		writeRolesErr(w, err)
		return
	}
	users, err := models.ListUsersByTenant(r.Context(), a.pool, caller.TenantID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not load users"})
		return
	}

	// Build userID -> role names by walking each role's members once. Avoids
	// a per-user role query and yields the per-role member counts for free.
	rolesByUser := make(map[string][]string)
	resp := rolesResponse{
		Roles: make([]roleInfo, 0, len(roles)),
		Users: make([]roleUserInfo, 0, len(users)),
	}
	for _, role := range roles {
		members, err := svc.ListMembers(r.Context(), caller, role.Name)
		if err != nil {
			writeRolesErr(w, err)
			return
		}
		for _, m := range members {
			rolesByUser[m.ID.String()] = append(rolesByUser[m.ID.String()], role.Name)
		}
		desc := ""
		if role.Description != nil {
			desc = *role.Description
		}
		resp.Roles = append(resp.Roles, roleInfo{
			Name:        role.Name,
			Description: desc,
			MemberCount: len(members),
		})
	}

	for _, u := range users {
		name := u.SlackUserID
		if u.DisplayName != nil && *u.DisplayName != "" {
			name = *u.DisplayName
		}
		held := rolesByUser[u.ID.String()]
		sort.Strings(held)
		resp.Users = append(resp.Users, roleUserInfo{
			UserID:      u.ID.String(),
			SlackUserID: u.SlackUserID,
			DisplayName: name,
			Roles:       held,
		})
	}
	sort.Slice(resp.Users, func(i, j int) bool {
		return resp.Users[i].DisplayName < resp.Users[j].DisplayName
	})

	writeJSON(w, http.StatusOK, resp)
}

type roleAssignBody struct {
	SlackUserID string `json:"slack_user_id"`
	RoleName    string `json:"role_name"`
}

func (a *App) handleRoleAssign(w http.ResponseWriter, r *http.Request) {
	a.roleMembership(w, r, true)
}

func (a *App) handleRoleUnassign(w http.ResponseWriter, r *http.Request) {
	a.roleMembership(w, r, false)
}

// roleMembership assigns (assign=true) or removes (assign=false) a role from
// a user, identified by Slack user ID to match the RoleService API.
func (a *App) roleMembership(w http.ResponseWriter, r *http.Request, assign bool) {
	caller := auth.CallerFromContext(r.Context())
	if caller == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var body roleAssignBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
		return
	}
	if body.SlackUserID == "" || body.RoleName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "slack_user_id and role_name are required"})
		return
	}
	svc := services.NewRoleService(a.pool)
	var err error
	if assign {
		err = svc.Assign(r.Context(), caller, body.SlackUserID, body.RoleName)
	} else {
		err = svc.Unassign(r.Context(), caller, body.SlackUserID, body.RoleName)
	}
	if err != nil {
		writeRolesErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeRolesErr maps a RoleService error to a status + JSON message.
func writeRolesErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, services.ErrForbidden):
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "Permission denied."})
	case errors.Is(err, services.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "User not found."})
	case errors.Is(err, services.ErrRoleNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "Role does not exist."})
	case errors.Is(err, services.ErrLastAdmin):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Can't remove the last admin."})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
	}
}
