package integrations

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
)

// MintSetupURL creates a pending integration for (provider, authType) scoped
// to the caller and returns a single-use setup URL where the secret fields
// are entered. It's the entry point behind a "Connect" button on the console
// Integrations page. Tenant-scoped types require an admin caller
// (services.ErrForbidden otherwise); user-scoped types are self-service.
func MintSetupURL(ctx context.Context, caller *services.Caller, provider, authType string) (string, error) {
	if instance == nil || instance.pool == nil {
		return "", errors.New("integrations app not initialized")
	}
	provider = strings.TrimSpace(provider)
	authType = strings.TrimSpace(authType)
	spec, ok := LookupTypeSpec(provider, authType)
	if !ok {
		return "", fmt.Errorf("no integration type %q registered", typeKey(provider, authType))
	}
	if spec.Scope == ScopeTenant && !caller.IsAdmin {
		return "", services.ErrForbidden
	}
	var targetUser *uuid.UUID
	if spec.Scope == ScopeUser {
		uid := caller.UserID
		targetUser = &uid
	}
	p, err := models.CreatePendingIntegration(
		ctx, instance.pool,
		caller.TenantID, caller.UserID,
		spec.Provider, spec.AuthType,
		targetUser,
		instance.tokenTTL(),
	)
	if err != nil {
		return "", fmt.Errorf("creating pending integration: %w", err)
	}
	return instance.buildSetupURL(ctx, p)
}

// TypeStatus describes one registered integration type plus the caller's
// connection state for it. Drives the console Integrations page's connect UI.
type TypeStatus struct {
	Provider      string `json:"provider"`
	AuthType      string `json:"auth_type"`
	DisplayName   string `json:"display_name"`
	Description   string `json:"description"`
	Scope         string `json:"scope"` // "tenant" | "user"
	Connected     bool   `json:"connected"`
	IntegrationID string `json:"integration_id"` // "" when not connected
	// CanManage is whether this caller may connect/delete it: always true for
	// user-scoped types (their own), admin-only for tenant-scoped types.
	CanManage bool `json:"can_manage"`
}

// CatalogFor returns every registered integration type with the caller's
// connection state. User-scoped types check the caller's own row; tenant-
// scoped types check the shared workspace row.
func CatalogFor(ctx context.Context, caller *services.Caller) ([]TypeStatus, error) {
	if instance == nil || instance.pool == nil {
		return nil, errors.New("integrations app not initialized")
	}
	specs := allTypeSpecs()
	sort.Slice(specs, func(i, j int) bool { return specs[i].DisplayName < specs[j].DisplayName })

	out := make([]TypeStatus, 0, len(specs))
	for _, s := range specs {
		var targetUser *uuid.UUID
		if s.Scope == ScopeUser {
			uid := caller.UserID
			targetUser = &uid
		}
		integ, err := models.GetIntegration(ctx, instance.pool, caller.TenantID, s.Provider, s.AuthType, targetUser)
		if err != nil {
			return nil, fmt.Errorf("loading integration state for %s: %w", s.Key(), err)
		}
		ts := TypeStatus{
			Provider:    s.Provider,
			AuthType:    s.AuthType,
			DisplayName: s.DisplayName,
			Description: s.Description,
			Scope:       string(s.Scope),
			CanManage:   s.Scope == ScopeUser || caller.IsAdmin,
		}
		if integ != nil {
			ts.Connected = true
			ts.IntegrationID = integ.ID.String()
		}
		out = append(out, ts)
	}
	return out, nil
}

// DeleteIntegrationForCaller removes an integration the caller is allowed to
// delete (their own, or any in-tenant row for admins). Wraps
// models.DeleteIntegration's ownership check.
func DeleteIntegrationForCaller(ctx context.Context, caller *services.Caller, id uuid.UUID) error {
	if instance == nil || instance.pool == nil {
		return errors.New("integrations app not initialized")
	}
	return models.DeleteIntegration(ctx, instance.pool, caller.TenantID, id, caller.UserID, caller.IsAdmin)
}
