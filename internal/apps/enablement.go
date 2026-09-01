package apps

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
)

// ErrNotToggleable is returned when a caller tries to enable/disable an app
// that isn't a user-facing feature (core infrastructure like console, admin,
// attachment, cards, integrations).
var ErrNotToggleable = errors.New("app is not toggleable")

// ErrEnablementUnconfigured is returned when SetEnabled runs before Init.
var ErrEnablementUnconfigured = errors.New("app enablement is not configured")

// Canonical core app names, referenced by the owning packages' Name() so the
// strings live in one place. These are core infrastructure (never toggleable),
// but so are attachment/cards/integrations — coreness is determined by NOT
// declaring feature metadata (see featureApps), not by this list.
const (
	AppConsole = "console"
	AppAdmin   = "admin"
)

// featureApps is the set of toggleable app names. An app is a user-facing
// feature iff it implements DescribableApp (declares DisplayName/Description);
// everything else is core infrastructure that is always on and never shown in
// the Apps settings page. Built once in initEnablement from the registry.
var featureApps map[string]bool

// IsToggleable reports whether an app can be enabled/disabled per tenant.
func IsToggleable(name string) bool { return featureApps[name] }

// enablementService caches each tenant's explicitly-disabled app set. Default
// is enabled (opt-out): a feature is on unless an explicit disabled row exists.
// The cache is busted on SetEnabled; in a single web process (the Dokku
// deploy) every write goes through here, so the cache stays consistent.
type enablementService struct {
	pool *pgxpool.Pool

	mu       sync.RWMutex
	disabled map[uuid.UUID]map[string]bool // tenant -> set of disabled app names
}

// enablement is the package-level singleton, wired in Init.
var enablement *enablementService

// initEnablement is called from Init once the pool is available. It also
// computes the toggleable-feature set from the registered apps.
func initEnablement(pool *pgxpool.Pool) {
	enablement = &enablementService{
		pool:     pool,
		disabled: make(map[uuid.UUID]map[string]bool),
	}
	featureApps = make(map[string]bool)
	for _, a := range registry {
		if _, ok := a.(DescribableApp); ok {
			featureApps[a.Name()] = true
		}
	}
}

// IsEnabled reports whether appName is enabled for the tenant. Non-feature
// (core) apps are always enabled. When the gate is unconfigured (test paths
// that skip Init) or the tenant is the zero UUID (no caller), everything is
// treated as enabled so non-tenant contexts behave as before. DB errors fail
// open (logged) — a transient lookup failure must not make a tenant's apps
// vanish.
func IsEnabled(ctx context.Context, tenantID uuid.UUID, appName string) bool {
	if !featureApps[appName] {
		return true
	}
	if enablement == nil || tenantID == uuid.Nil {
		return true
	}
	return !enablement.isDisabled(ctx, tenantID, appName)
}

func (s *enablementService) isDisabled(ctx context.Context, tenantID uuid.UUID, appName string) bool {
	set, ok := s.cached(tenantID)
	if !ok {
		var err error
		set, err = s.load(ctx, tenantID)
		if err != nil {
			slog.Error("loading app enablement; defaulting to enabled", "tenant_id", tenantID, "error", err)
			return false
		}
	}
	return set[appName]
}

func (s *enablementService) cached(tenantID uuid.UUID) (map[string]bool, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	set, ok := s.disabled[tenantID]
	return set, ok
}

func (s *enablementService) load(ctx context.Context, tenantID uuid.UUID) (map[string]bool, error) {
	names, err := models.DisabledApps(ctx, s.pool, tenantID)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	s.mu.Lock()
	s.disabled[tenantID] = set
	s.mu.Unlock()
	return set, nil
}

// DisabledApps returns the tenant's explicitly-disabled app names (cached).
// Used by the admin settings API to render current state.
func DisabledApps(ctx context.Context, tenantID uuid.UUID) (map[string]bool, error) {
	if enablement == nil {
		return map[string]bool{}, nil
	}
	if set, ok := enablement.cached(tenantID); ok {
		return set, nil
	}
	return enablement.load(ctx, tenantID)
}

// AppInfo describes a toggleable feature app for the admin enablement UI.
type AppInfo struct {
	Name        string
	DisplayName string
	Description string
}

// Catalog returns metadata for every toggleable feature app (those declaring
// DescribableApp). Core infrastructure apps are intentionally omitted.
func Catalog() []AppInfo {
	out := make([]AppInfo, 0, len(registry))
	for _, a := range registry {
		if !featureApps[a.Name()] {
			continue
		}
		info := AppInfo{Name: a.Name(), DisplayName: titleCase(a.Name())}
		if d, ok := a.(DescribableApp); ok {
			if dn := d.DisplayName(); dn != "" {
				info.DisplayName = dn
			}
			info.Description = d.Description()
		}
		out = append(out, info)
	}
	return out
}

// titleCase turns an app slug ("vault", "kiosk") into a display fallback
// ("Vault", "Kiosk"). Apps wanting nicer casing implement DescribableApp.
func titleCase(name string) string {
	if name == "" {
		return ""
	}
	s := strings.ReplaceAll(name, "_", " ")
	return strings.ToUpper(s[:1]) + s[1:]
}

// SetEnabled persists an admin's toggle and busts the tenant's cache. Refuses
// non-feature (core) apps so infrastructure can never be turned off.
func SetEnabled(ctx context.Context, tenantID uuid.UUID, appName string, enabled bool) error {
	if !featureApps[appName] {
		return ErrNotToggleable
	}
	if enablement == nil {
		return ErrEnablementUnconfigured
	}
	if err := models.SetAppEnabled(ctx, enablement.pool, tenantID, appName, enabled); err != nil {
		return err
	}
	enablement.mu.Lock()
	delete(enablement.disabled, tenantID)
	enablement.mu.Unlock()
	return nil
}
