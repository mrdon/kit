package googlecalendar

import "github.com/google/uuid"

// Private extended-property keys that mark an event as written by one Kit
// feature on behalf of one tenant. Private properties are only visible on
// our copy of the event and are queryable via privateExtendedProperty, so
// they're the durable record of "we wrote this".
//
// Every integration that writes events MUST stamp both. Cleanup sweeps use
// them to separate the two questions that must never be conflated:
//
//	ownership — did this feature write this event? (stamp matches)
//	staleness — is it still backed by a live source record? (caller's model)
//
// Only an event that is both owned and stale may be deleted. An event
// without a matching stamp — a human's meeting, another Kit feature's
// event, anything at all — is invisible to the sweep and must never be
// touched, even if it sits in the middle of the sync window.
const (
	PropApp    = "kitApp"
	PropTenant = "kitTenantId"
)

// OwnerProps returns the ownership stamp for events written by appName for
// tenantID. Merge it into an event's private properties on write, and pass
// the same map to ListEventsByPrivateProperties to read back exactly the
// events this feature owns — nothing more.
//
// appName should be the app's registry name (e.g. "square_shifts") so two
// features writing to the same calendar never claim each other's events.
func OwnerProps(appName string, tenantID uuid.UUID) map[string]string {
	return map[string]string{
		PropApp:    appName,
		PropTenant: tenantID.String(),
	}
}
