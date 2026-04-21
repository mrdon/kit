package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
	"github.com/mrdon/kit/internal/tools"
)

func registerIntegrationAgentTools(r *tools.Registry, a *App) {
	for _, meta := range services.IntegrationTools {
		handler := agentHandler(a, meta.Name)
		if handler == nil {
			continue
		}
		r.Register(tools.Def{
			Name:        meta.Name,
			Description: meta.Description,
			Schema:      meta.Schema,
			AdminOnly:   meta.AdminOnly,
			Handler:     handler,
		})
	}
}

func agentHandler(a *App, name string) tools.HandlerFunc {
	switch name {
	case "configure_integration":
		return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
			var inp struct {
				Provider string `json:"provider"`
				AuthType string `json:"auth_type"`
			}
			if err := json.Unmarshal(input, &inp); err != nil {
				return "", fmt.Errorf("parsing input: %w", err)
			}
			msg, err := a.configureIntegration(ec.Ctx, ec.Caller(), inp.Provider, inp.AuthType)
			if err != nil {
				return friendlyErr(err), nil
			}
			return msg, nil
		}
	case "check_integration_status":
		return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
			var inp struct {
				PendingID string `json:"pending_id"`
			}
			if err := json.Unmarshal(input, &inp); err != nil {
				return "", fmt.Errorf("parsing input: %w", err)
			}
			id, err := uuid.Parse(inp.PendingID)
			if err != nil {
				return "Invalid pending_id UUID.", nil
			}
			msg, err := a.checkStatus(ec.Ctx, ec.Caller(), id)
			if err != nil {
				return friendlyErr(err), nil
			}
			return msg, nil
		}
	case "list_integrations":
		return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
			var inp struct {
				All bool `json:"all"`
			}
			if len(input) > 0 {
				if err := json.Unmarshal(input, &inp); err != nil {
					return "", fmt.Errorf("parsing input: %w", err)
				}
			}
			msg, err := a.listIntegrations(ec.Ctx, ec.Caller(), inp.All)
			if err != nil {
				return friendlyErr(err), nil
			}
			return msg, nil
		}
	case "delete_integration":
		return func(ec *tools.ExecContext, input json.RawMessage) (string, error) {
			var inp struct {
				IntegrationID string `json:"integration_id"`
			}
			if err := json.Unmarshal(input, &inp); err != nil {
				return "", fmt.Errorf("parsing input: %w", err)
			}
			id, err := uuid.Parse(inp.IntegrationID)
			if err != nil {
				return "Invalid integration_id UUID.", nil
			}
			msg, err := a.deleteIntegration(ec.Ctx, ec.Caller(), id)
			if err != nil {
				return friendlyErr(err), nil
			}
			return msg, nil
		}
	case "list_integration_types":
		return func(_ *tools.ExecContext, _ json.RawMessage) (string, error) {
			return renderTypeCatalog(), nil
		}
	}
	return nil
}

// configureIntegration validates the request, creates a pending row, and
// returns a user-facing message containing the signed URL and pending_id.
func (a *App) configureIntegration(ctx context.Context, caller *services.Caller, provider, authType string) (string, error) {
	provider = strings.TrimSpace(provider)
	authType = strings.TrimSpace(authType)
	if provider == "" || authType == "" {
		return "provider and auth_type are required.", nil
	}
	spec, ok := LookupTypeSpec(provider, authType)
	if !ok {
		return fmt.Sprintf("No integration type %q is registered. Use list_integration_types to see what's available.", typeKey(provider, authType)), nil
	}
	if spec.Scope == ScopeTenant && !caller.IsAdmin {
		return spec.DisplayName + " is a workspace-wide integration; only admins can configure it.", nil
	}

	if a.pool == nil {
		return "", errors.New("integrations app not initialized")
	}

	var targetUser *uuid.UUID
	if spec.Scope == ScopeUser {
		uid := caller.UserID
		targetUser = &uid
	}

	p, err := models.CreatePendingIntegration(
		ctx, a.pool,
		caller.TenantID, caller.UserID,
		spec.Provider, spec.AuthType,
		targetUser,
		a.tokenTTL(),
	)
	if err != nil {
		return "", fmt.Errorf("creating pending integration: %w", err)
	}

	url, err := a.buildSetupURL(ctx, p)
	if err != nil {
		return "", fmt.Errorf("building setup url: %w", err)
	}

	return fmt.Sprintf(
		"To configure %s, visit: %s\n\nThe link is single-use and expires in %d minutes. Once the user has submitted the form, call check_integration_status with pending_id=%s.",
		spec.DisplayName, url, int(a.tokenTTL().Minutes()), p.ID,
	), nil
}

// checkStatus returns the current state of a pending integration.
func (a *App) checkStatus(ctx context.Context, caller *services.Caller, pendingID uuid.UUID) (string, error) {
	if a.pool == nil {
		return "", errors.New("integrations app not initialized")
	}
	p, err := loadPendingAny(ctx, a.pool, caller.TenantID, pendingID)
	if err != nil {
		return "", err
	}
	if p == nil {
		return fmt.Sprintf("No pending integration with id %s (may have expired).", pendingID), nil
	}
	if !callerMayViewPending(caller, p) {
		return "You don't have access to this pending integration.", nil
	}

	switch p.Status {
	case models.PendingStatusPending:
		return fmt.Sprintf("Pending: user hasn't submitted the form yet. (%s)", typeKey(p.Provider, p.AuthType)), nil
	case models.PendingStatusConsumed:
		var userFilter *uuid.UUID
		if p.TargetUserID != nil {
			uid := *p.TargetUserID
			userFilter = &uid
		}
		integ, err := models.GetIntegration(ctx, a.pool, caller.TenantID, p.Provider, p.AuthType, userFilter)
		if err != nil {
			return "", fmt.Errorf("loading integration after consume: %w", err)
		}
		if integ == nil {
			return "Consumed, but no integration row found (was it deleted?).", nil
		}
		return fmt.Sprintf("Ready. integration_id=%s (%s)", integ.ID, typeKey(p.Provider, p.AuthType)), nil
	case models.PendingStatusExpired:
		return fmt.Sprintf("Expired. (%s)", typeKey(p.Provider, p.AuthType)), nil
	default:
		return fmt.Sprintf("Status: %s", p.Status), nil
	}
}

// listIntegrations formats the caller's visible integrations.
func (a *App) listIntegrations(ctx context.Context, caller *services.Caller, all bool) (string, error) {
	if a.pool == nil {
		return "", errors.New("integrations app not initialized")
	}
	includeAll := all && caller.IsAdmin
	uid := caller.UserID
	integs, err := models.ListIntegrations(ctx, a.pool, caller.TenantID, &uid, includeAll)
	if err != nil {
		return "", fmt.Errorf("listing integrations: %w", err)
	}
	if len(integs) == 0 {
		return "No integrations configured.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Found %d integration(s):\n\n", len(integs))
	for _, i := range integs {
		scope := "tenant"
		owner := "workspace"
		if i.UserID != nil {
			scope = "user"
			owner = i.UserID.String()
		}
		fmt.Fprintf(&b, "• %s [%s] — id=%s, scope=%s, owner=%s",
			typeKey(i.Provider, i.AuthType), displayName(i.Provider, i.AuthType),
			i.ID, scope, owner)
		if i.Username != "" {
			fmt.Fprintf(&b, ", username=%s", i.Username)
		}
		if len(i.Config) > 0 {
			cfg, _ := json.Marshal(i.Config)
			fmt.Fprintf(&b, ", config=%s", cfg)
		}
		fmt.Fprintf(&b, "\n")
	}
	return b.String(), nil
}

// deleteIntegration removes an integration row, enforcing ownership.
func (a *App) deleteIntegration(ctx context.Context, caller *services.Caller, id uuid.UUID) (string, error) {
	if a.pool == nil {
		return "", errors.New("integrations app not initialized")
	}
	integ, err := models.GetIntegrationByID(ctx, a.pool, caller.TenantID, id)
	if err != nil {
		return "", fmt.Errorf("loading integration: %w", err)
	}
	if integ == nil {
		return "Integration not found.", nil
	}
	err = models.DeleteIntegration(ctx, a.pool, caller.TenantID, id, caller.UserID, caller.IsAdmin)
	if errors.Is(err, models.ErrIntegrationForbidden) {
		return "You don't have permission to delete that integration.", nil
	}
	if err != nil {
		return "", fmt.Errorf("deleting integration: %w", err)
	}
	return fmt.Sprintf("Deleted %s (%s).", id, typeKey(integ.Provider, integ.AuthType)), nil
}

// renderTypeCatalog pretty-prints the registered TypeSpecs for the LLM.
func renderTypeCatalog() string {
	specs := allTypeSpecs()
	if len(specs) == 0 {
		return "No integration types are currently registered."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Available integration types:\n\n")
	for _, s := range specs {
		fmt.Fprintf(&b, "• %s — %s [scope=%s]", s.Key(), s.DisplayName, s.Scope)
		if s.Description != "" {
			fmt.Fprintf(&b, "\n   %s", s.Description)
		}
		if len(s.Fields) > 0 {
			fmt.Fprintf(&b, "\n   Fields:")
			for _, f := range s.Fields {
				marker := ""
				if f.IsSecret() {
					marker = " (secret)"
				}
				req := ""
				if f.Required {
					req = " required"
				}
				fmt.Fprintf(&b, "\n     - %s%s%s", f.Name, req, marker)
			}
		}
		fmt.Fprintf(&b, "\n\n")
	}
	return b.String()
}

// loadPendingAny loads a pending row (including consumed ones, as long as
// expires_at hasn't passed). Delegates to models.GetPendingIntegration,
// which already treats not-found as (nil, nil).
func loadPendingAny(ctx context.Context, pool *pgxpool.Pool, tenantID, id uuid.UUID) (*models.PendingIntegration, error) {
	return models.GetPendingIntegration(ctx, pool, tenantID, id)
}

// callerMayViewPending returns whether the caller is allowed to inspect a
// pending row: must be the creator, the target user, or an admin.
func callerMayViewPending(caller *services.Caller, p *models.PendingIntegration) bool {
	if caller.IsAdmin {
		return true
	}
	if p.CreatedBy == caller.UserID {
		return true
	}
	if p.TargetUserID != nil && *p.TargetUserID == caller.UserID {
		return true
	}
	return false
}

// displayName returns the TypeSpec display name if registered, else a
// fallback so list_integrations output stays useful even for types that
// were unregistered (or never registered on this build).
func displayName(provider, authType string) string {
	if spec, ok := LookupTypeSpec(provider, authType); ok {
		return spec.DisplayName
	}
	return typeKey(provider, authType)
}

// friendlyErr formats an internal error into a user-visible string for
// tool output. Handlers use this for non-fatal errors so the LLM sees a
// readable message instead of a Go error chain.
func friendlyErr(err error) string {
	return "Error: " + err.Error()
}
