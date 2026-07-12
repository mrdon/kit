// Django-style "urls.go" — maps HTTP paths to handlers for the console.
package console

import (
	"net/http"

	"github.com/mrdon/kit/internal/apps"
	consoleweb "github.com/mrdon/kit/web/console"
)

func registerConsoleRoutes(mux apps.Mux, a *App) {
	page := func(h http.HandlerFunc) http.Handler { return PageRoute(a.pool, a.signer, h) }
	jsonRoute := func(h http.HandlerFunc) http.Handler { return JSON(a.pool, a.signer, h) }
	adminJSON := func(h http.HandlerFunc) http.Handler { return AdminJSON(a.pool, a.signer, h) }

	// Shared console bundle — one copy serves every workspace.
	mux.Handle("GET /console/assets/", consoleweb.AssetHandler())
	// The vault SharedWorker is emitted unhashed at the dist root (it
	// needs a stable URL); served here, never under /console/assets/.
	mux.Handle("GET /console/vault-worker.js", consoleweb.StaticFileHandler(
		"vault-worker.js", "application/javascript; charset=utf-8"))

	// Shell: the index and every client-side route serve the same SPA
	// HTML. The {rest...} wildcard covers /{slug}/web/ and any deeper
	// client route (e.g. /{slug}/web/admin/integrations) on reload.
	mux.Handle("GET /{slug}/"+Segment, page(a.handleShell))
	mux.Handle("GET /{slug}/"+Segment+"/{rest...}", page(a.handleShell))

	// JSON API owned by the console itself. The API lives under
	// /{slug}/api/... (NOT under the /web shell prefix) — it's reached by
	// fetch, never navigated to, and the cards service worker already
	// skips anything containing /api/, so it's cache-safe for free.
	// Feature apps register their own /{slug}/api/... routes the same way.
	mux.Handle("GET /{slug}/api/me", jsonRoute(a.handleMe))
	mux.Handle("GET /{slug}/api/integrations", adminJSON(a.handleIntegrations))

	// Integration connect/manage. Caller-scoped (not admin-only) so
	// user-scoped connectors (email) can be self-served; MintSetupURL and
	// the delete ownership check enforce admin for workspace-wide types.
	mux.Handle("GET /{slug}/api/integration-catalog", jsonRoute(a.handleIntegrationCatalog))
	mux.Handle("POST /{slug}/api/integration-catalog/connect", jsonRoute(a.handleIntegrationConnect))
	mux.Handle("DELETE /{slug}/api/integrations/{id}", jsonRoute(a.handleIntegrationDelete))

	// App enable/disable. Admin-only: an admin turns feature apps on or off
	// for the whole workspace. Core apps (console, admin) reject the PUT.
	mux.Handle("GET /{slug}/api/apps", adminJSON(a.handleAppsList))
	mux.Handle("PUT /{slug}/api/apps/{name}", adminJSON(a.handleAppSet))

	// Role membership matrix. Admin-only: the page reveals every user and
	// lets an admin change who is in which role.
	mux.Handle("GET /{slug}/api/roles", adminJSON(a.handleRoles))
	mux.Handle("POST /{slug}/api/roles/assign", adminJSON(a.handleRoleAssign))
	mux.Handle("POST /{slug}/api/roles/unassign", adminJSON(a.handleRoleUnassign))

	// Skills. Listing/loading is scope-filtered by the service and open to
	// any caller; mutations + file management are admin-only (AdminJSON here
	// AND independently enforced by SkillService).
	mux.Handle("GET /{slug}/api/skills", jsonRoute(a.handleSkillsList))
	mux.Handle("GET /{slug}/api/skills/meta", jsonRoute(a.handleSkillsMeta))
	mux.Handle("POST /{slug}/api/skills", adminJSON(a.handleSkillCreate))
	mux.Handle("GET /{slug}/api/skills/{id}", jsonRoute(a.handleSkillGet))
	mux.Handle("PATCH /{slug}/api/skills/{id}", adminJSON(a.handleSkillUpdate))
	mux.Handle("DELETE /{slug}/api/skills/{id}", adminJSON(a.handleSkillDelete))
	mux.Handle("GET /{slug}/api/skills/{id}/files", adminJSON(a.handleSkillFilesList))
	mux.Handle("POST /{slug}/api/skills/{id}/files", adminJSON(a.handleSkillFileAdd))
	mux.Handle("DELETE /{slug}/api/skills/files/{fileId}", adminJSON(a.handleSkillFileDelete))

	// Jobs. Visibility + the right to manage a given job are enforced by
	// JobService (non-admins scope-limited, admins tenant-wide), so these are
	// plain JSON routes — never AdminJSON.
	mux.Handle("GET /{slug}/api/jobs", jsonRoute(a.handleJobsList))
	mux.Handle("GET /{slug}/api/jobs/meta", jsonRoute(a.handleJobsMeta))
	mux.Handle("GET /{slug}/api/jobs/{id}", jsonRoute(a.handleJobGet))
	mux.Handle("PATCH /{slug}/api/jobs/{id}", jsonRoute(a.handleJobUpdate))
	mux.Handle("DELETE /{slug}/api/jobs/{id}", jsonRoute(a.handleJobDelete))
}
