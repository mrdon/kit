package console

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	kitslack "github.com/mrdon/kit/internal/slack"
)

// The roles page is the admin surface for "who is in which role": a matrix
// of every role against every workspace member, with assign/unassign
// toggling membership. These handlers are thin — all role logic (effective
// roles, the Slack roster, the default-role fallback, the assign/unassign
// guards) lives in services.RoleService, shared with the MCP/agent tools.

type roleInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	MemberCount int    `json:"member_count"`
	Catchall    bool   `json:"catchall"`
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

// handleRoles returns the full role/user matrix from the service and maps it
// to the JSON the page renders. No business logic here.
func (a *App) handleRoles(w http.ResponseWriter, r *http.Request) {
	caller := auth.CallerFromContext(r.Context())
	if caller == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	m, err := services.NewRoleService(a.pool).Membership(r.Context(), caller, a.slackRoster(caller.TenantID))
	if err != nil {
		writeRolesErr(w, err)
		return
	}

	resp := rolesResponse{
		Roles: make([]roleInfo, 0, len(m.Roles)),
		Users: make([]roleUserInfo, 0, len(m.Users)),
	}
	for _, role := range m.Roles {
		resp.Roles = append(resp.Roles, roleInfo{
			Name:        role.Name,
			Description: role.Description,
			MemberCount: role.MemberCount,
			Catchall:    role.Catchall,
		})
	}
	for _, u := range m.Users {
		roles := u.Roles
		if roles == nil {
			roles = []string{}
		}
		resp.Users = append(resp.Users, roleUserInfo{
			UserID:      u.UserID,
			SlackUserID: u.SlackUserID,
			DisplayName: u.DisplayName,
			Roles:       roles,
		})
	}
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

// slackRoster builds a WorkspaceLister that returns the tenant's full Slack
// member list, so the roles matrix shows everyone (not just users who've used
// Kit). Fetching it — token decryption + the Slack API — is infrastructure,
// so it lives here in the console rather than in the role service. Returns
// nil if no encryptor is wired; Membership then falls back to known users.
func (a *App) slackRoster(tenantID uuid.UUID) services.WorkspaceLister {
	if a.enc == nil {
		return nil
	}
	return func(ctx context.Context) ([]services.WorkspacePerson, error) {
		tenant, err := models.GetTenantByID(ctx, a.pool, tenantID)
		if err != nil {
			return nil, err
		}
		if tenant == nil || tenant.BotToken == "" {
			return nil, fmt.Errorf("no bot token for tenant %s", tenantID)
		}
		token, err := a.enc.Decrypt(tenant.BotToken)
		if err != nil {
			return nil, fmt.Errorf("decrypting bot token: %w", err)
		}
		infos, err := kitslack.NewClient(token).ListAllUsers(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing slack users: %w", err)
		}
		people := make([]services.WorkspacePerson, 0, len(infos))
		for _, in := range infos {
			people = append(people, services.WorkspacePerson{
				SlackUserID: in.SlackUserID,
				DisplayName: in.DisplayName,
			})
		}
		return people, nil
	}
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
	case errors.Is(err, services.ErrCannotLeaveMember):
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Everyone is a member; that role can't be changed."})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal error"})
	}
}
