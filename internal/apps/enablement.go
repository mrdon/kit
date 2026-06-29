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

// ErrCoreApp is returned when a caller tries to toggle a core app.
var ErrCoreApp = errors.New("core apps cannot be disabled")

// ErrEnablementUnconfigured is returned when SetEnabled runs before Init.
var ErrEnablementUnconfigured = errors.New("app enablement is not configured")

// Core app names. These host the web console and the admin tooling (including
// the apps-enablement page itself), so they can never be disabled. The owning
// packages reference these in their Name() to keep a single source of truth.
const (
	AppConsole = "console"
	AppAdmin   = "admin"
)

// CoreApps are never disableable. IsEnabled always reports them enabled, and
// SetEnabled refuses to write them.
var CoreApps = map[string]bool{
	AppConsole: true,
	AppAdmin:   true,
}

// IsCoreApp reports whether an app is load-bearing and cannot be toggled.
func IsCoreApp(name string) bool { return CoreApps[name] }

// enablementService caches each tenant's explicitly-disabled app set. Default
// is enabled (opt-out): an app is on unless an explicit disabled row exists.
// The cache is busted on SetEnabled; in a single web process (the Dokku
// deploy) every write goes through here, so the cache stays consistent.
type enablementService struct {
	pool *pgxpool.Pool

	mu       sync.RWMutex
	disabled map[uuid.UUID]map[string]bool // tenant -> set of disabled app names
}

// enablement is the package-level singleton, wired in Init.
var enablement *enablementService

// initEnablement is called from Init once the pool is available.
func initEnablement(pool *pgxpool.Pool) {
	enablement = &enablementService{
		pool:     pool,
		disabled: make(map[uuid.UUID]map[string]bool),
	}
}

// IsEnabled reports whether appName is enabled for the tenant. Core apps are
// always enabled. When the gate is unconfigured (test paths that skip Init) or
// the tenant is the zero UUID (no caller), everything is treated as enabled so
// non-tenant contexts behave as before. DB errors fail open (logged) — a
// transient lookup failure must not make a tenant's apps vanish.
func IsEnabled(ctx context.Context, tenantID uuid.UUID, appName string) bool {
	if CoreApps[appName] {
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

// AppInfo describes a registered app for the admin enablement UI.
type AppInfo struct {
	Name        string
	DisplayName string
	Description string
	Core        bool
}

// Catalog returns metadata for every registered app, using DescribableApp
// when an app implements it and falling back to a title-cased Name otherwise.
func Catalog() []AppInfo {
	out := make([]AppInfo, 0, len(registry))
	for _, a := range registry {
		info := AppInfo{
			Name:        a.Name(),
			DisplayName: titleCase(a.Name()),
			Core:        CoreApps[a.Name()],
		}
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

// titleCase turns an app slug ("expense", "github") into a display fallback
// ("Expense", "Github"). Apps wanting nicer casing implement DescribableApp.
func titleCase(name string) string {
	if name == "" {
		return ""
	}
	s := strings.ReplaceAll(name, "_", " ")
	return strings.ToUpper(s[:1]) + s[1:]
}

// SetEnabled persists an admin's toggle and busts the tenant's cache. Refuses
// core apps so the console/admin surfaces can never be turned off.
func SetEnabled(ctx context.Context, tenantID uuid.UUID, appName string, enabled bool) error {
	if CoreApps[appName] {
		return ErrCoreApp
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
