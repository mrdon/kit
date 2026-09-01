package admin

import (
	"context"
	"sync"

	"github.com/google/uuid"
)

// Status is what an Integration reports for one tenant. Detail is a
// short human-readable string rendered next to the status pill —
// "site: brewery-marketing", "installation on twdata-org", etc.
type Status struct {
	Connected bool
	Detail    string
}

// Integration is the contract each app implements to surface itself
// on the admin integrations index. Implementations register
// themselves via RegisterIntegration from their package init() or
// Configure() — admin doesn't import any other app package.
type Integration interface {
	// Name is the display name shown as the card heading.
	Name() string

	// Description is a one-line explanation rendered under the name.
	Description() string

	// Slug is a stable identifier used for sort order + as the
	// element id. Lowercase, kebab-case. "vault", "events".
	Slug() string

	// Status reports per-tenant connection state.
	Status(ctx context.Context, tenantID uuid.UUID) (Status, error)

	// ManageURL returns the per-tenant settings/connect URL the
	// "Manage" button links to. Empty string disables the button
	// (useful for substrate integrations that have no UI of their
	// own — like the github app today).
	ManageURL(tenantSlug string) string
}

var (
	mu           sync.RWMutex
	integrations []Integration
)

// RegisterIntegration adds an integration to the index. Called by
// each app from its init() or Configure(). Idempotent on slug —
// re-registering replaces the previous entry rather than
// duplicating, so Configure can be called more than once in tests.
func RegisterIntegration(i Integration) {
	if i == nil {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	slug := i.Slug()
	for idx, existing := range integrations {
		if existing.Slug() == slug {
			integrations[idx] = i
			return
		}
	}
	integrations = append(integrations, i)
}

// Integrations returns a snapshot of the registered integrations.
// Sorted by Slug for stable rendering.
func Integrations() []Integration {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Integration, len(integrations))
	copy(out, integrations)
	// Tiny list — insertion sort by slug is plenty.
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j].Slug() < out[i].Slug() {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
