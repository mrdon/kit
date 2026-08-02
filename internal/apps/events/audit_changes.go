package events

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/models"
)

// Per-event audit rows, and the "what has changed since the website was last
// built?" query that reads them.
//
// The website is static, so an edit in Kit is invisible on the web until a
// build runs. Rather than keeping a second copy of the feed to diff against,
// the pending list is derived from audit_events -- the history already exists,
// it is already namespaced per app, and deriving from it means the two can
// never disagree. It also survives the build, so it stays an audit trail
// instead of being consumed.
//
// AffectsSite is the load-bearing field. Most day-to-day activity -- private
// bookings, staff notes, headcounts, drafts being worked on -- changes nothing
// the public can see, and counting those would nag for builds that would
// produce a byte-identical site. It is computed at write time, when both the
// before and after states are known.

const (
	actionEventCreated     = "events.event_created"
	actionEventUpdated     = "events.event_updated"
	actionEventPublished   = "events.event_published"
	actionEventUnpublished = "events.event_unpublished"
	actionEventCancelled   = "events.event_cancelled"
	actionEventDeleted     = "events.event_deleted"
	actionSitePublished    = "events.site_published"
)

// changeMetadata is the typed payload for one event-level change.
type changeMetadata struct {
	Title string `json:"title"`
	Slug  string `json:"slug"`
	// AffectsSite is true when the change alters what the public feed emits,
	// so it is the only thing that should ever prompt a rebuild.
	AffectsSite bool `json:"affects_site"`
	// Actor is a display name where one is known; the audit row's
	// actor_user_id carries the authoritative id.
	Actor string `json:"actor,omitempty"`
}

// siteBuildMetadata records a website build request.
type siteBuildMetadata struct {
	TriggeredBy string `json:"triggered_by"`
	Changes     int    `json:"changes"`
	Error       string `json:"error,omitempty"`
}

// recordChange writes one event-level audit row.
//
// before may be nil (a creation). The public-visibility test looks at BOTH
// states: unpublishing a live event changes the site just as much as
// publishing one, and only comparing the new state would miss it.
func recordChange(ctx context.Context, pool *pgxpool.Pool, action string, before, after *Event) {
	if pool == nil || after == nil {
		return
	}
	affects := after.IsPubliclyVisible() || (before != nil && before.IsPubliclyVisible())
	// The caller rides in the request context, which every surface -- console,
	// agent, MCP -- already carries, so attribution needs no signature change.
	var actorID *uuid.UUID
	var actorName string
	if c := auth.CallerFromContext(ctx); c != nil {
		if c.UserID != uuid.Nil {
			id := c.UserID
			actorID = &id
		}
		actorName = c.Identity
	}
	if err := models.AppendAudit(ctx, pool, models.AuditEvent{
		TenantID:    after.TenantID,
		ActorUserID: actorID,
		Action:      action,
		TargetKind:  "app_event",
		TargetID:    &after.ID,
		Metadata: changeMetadata{
			Title:       after.Title,
			Slug:        after.Slug,
			AffectsSite: affects,
			Actor:       actorName,
		},
	}); err != nil {
		slog.Warn("events: recording change audit failed",
			"tenant_id", after.TenantID, "event_id", after.ID, "action", action, "error", err)
	}
}

// PendingChange is one site-affecting change awaiting a website build.
type PendingChange struct {
	Action    string    `json:"action"`
	Title     string    `json:"title"`
	Slug      string    `json:"slug"`
	Actor     string    `json:"actor,omitempty"`
	CreatedAt time.Time `json:"at"`
}

// Verb renders the action for a human, so the UI never parses the constant.
func (c PendingChange) Verb() string {
	switch c.Action {
	case actionEventCreated:
		return "added"
	case actionEventUpdated:
		return "edited"
	case actionEventPublished:
		return "published"
	case actionEventUnpublished:
		return "unpublished"
	case actionEventCancelled:
		return "cancelled"
	case actionEventDeleted:
		return "deleted"
	default:
		return "changed"
	}
}

// PendingChanges lists site-affecting changes made since the given time, newest
// first. A nil `since` means the site has never been built, so everything
// counts.
func (s *Service) PendingChanges(ctx context.Context, tenantID uuid.UUID, since *time.Time, limit int) ([]PendingChange, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	// A zero time rather than NULL keeps the comparison in one code path.
	cutoff := time.Time{}
	if since != nil {
		cutoff = *since
	}
	// Join users for a readable name: the audit row stores the caller's
	// identity, which for Slack is an opaque id like "U0B0H2SEYTC" -- accurate
	// and useless in a review list. Left join because the actor may be a
	// system action, or a user since removed.
	rows, err := s.pool.Query(ctx, `
		SELECT a.action, a.metadata, a.created_at, COALESCE(u.display_name, '')
		FROM audit_events a
		LEFT JOIN users u ON u.id = a.actor_user_id
		WHERE a.tenant_id = $1
		  AND a.action LIKE 'events.event_%'
		  AND a.created_at > $2
		  AND a.metadata->>'affects_site' = 'true'
		ORDER BY a.created_at DESC
		LIMIT $3`, tenantID, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []PendingChange{}
	for rows.Next() {
		var (
			c    PendingChange
			meta changeMetadata
		)
		var displayName string
		if err := rows.Scan(&c.Action, &meta, &c.CreatedAt, &displayName); err != nil {
			return nil, err
		}
		c.Title, c.Slug = meta.Title, meta.Slug
		// Prefer the resolved name; fall back to the stored identity so an
		// action is never left looking anonymous.
		c.Actor = displayName
		if c.Actor == "" {
			c.Actor = meta.Actor
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
