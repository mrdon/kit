package events

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/auth"
	"github.com/mrdon/kit/internal/models"
)

// Publishing to the website.
//
// The site is static, so Kit and the web are only in step just after a build.
// This is the button that puts them back in step, plus the answer to "what
// would that actually change?" -- so nobody has to guess whether it is worth
// pressing.
//
// The trigger is Netlify's build hook: a URL that starts a build when POSTed
// to. It carries its own secret in the path, so it is stored like any other
// credential and never echoed back to the browser.

// siteBuildTimeout bounds the hook call. Netlify returns as soon as the build
// is QUEUED, so this only needs to cover the handshake, not the build.
const siteBuildTimeout = 20 * time.Second

// SiteStatus is everything the admin page needs to decide whether to build.
type SiteStatus struct {
	HookConfigured bool            `json:"hook_configured"`
	BuiltAt        *time.Time      `json:"built_at,omitempty"`
	BuiltBy        string          `json:"built_by,omitempty"`
	Pending        []PendingChange `json:"pending"`
	// PendingTruncated reports that more changes exist than are listed, so the
	// UI can say "and N more" rather than implying the list is complete.
	PendingTruncated bool `json:"pending_truncated"`
}

const pendingListLimit = 50

// SiteStatus reports publish state and what is waiting to go out.
func (s *Service) SiteStatus(ctx context.Context, tenantID uuid.UUID) (SiteStatus, error) {
	settings, err := getSettings(ctx, s.pool, tenantID)
	if err != nil {
		return SiteStatus{}, err
	}
	// Ask for one more than we show, purely to know whether to say "and more".
	pending, err := s.PendingChanges(ctx, tenantID, settings.SiteBuiltAt, pendingListLimit+1)
	if err != nil {
		return SiteStatus{}, err
	}
	out := SiteStatus{
		HookConfigured: strings.TrimSpace(settings.SiteBuildHookURL) != "",
		BuiltAt:        settings.SiteBuiltAt,
		BuiltBy:        settings.SiteBuiltBy,
		Pending:        pending,
	}
	if len(pending) > pendingListLimit {
		out.Pending = pending[:pendingListLimit]
		out.PendingTruncated = true
	}
	return out, nil
}

// PublishSite asks the website to rebuild.
//
// The recorded build time is taken BEFORE the hook fires. Anything edited
// while the build runs would otherwise be swallowed -- marked as published
// when the build that started earlier never saw it -- and would then sit
// invisible on the web until something unrelated triggered the next build.
// Erring the other way just means one change is listed as pending twice.
func (s *Service) PublishSite(ctx context.Context, tenantID uuid.UUID, triggeredBy string) (SiteStatus, error) {
	settings, err := getSettings(ctx, s.pool, tenantID)
	if err != nil {
		return SiteStatus{}, err
	}
	hook := strings.TrimSpace(settings.SiteBuildHookURL)
	if hook == "" {
		return SiteStatus{}, invalid("no build hook is set. In Netlify: Site configuration → Build & deploy → Build hooks → Add build hook, then paste the URL here.")
	}

	pending, err := s.PendingChanges(ctx, tenantID, settings.SiteBuiltAt, pendingListLimit+1)
	if err != nil {
		return SiteStatus{}, err
	}
	// The mark comes from the DATABASE clock, not this process's, because it
	// is compared against audit_events.created_at, which the database stamps
	// with now(). Two clocks either side of a ">" is a bug even when they are
	// close: the gap between an edit and the build that follows it can be
	// under a millisecond, so a database running even slightly ahead makes an
	// already-published change read as still pending -- forever. The nightly
	// job would then rebuild the site every single night and never converge.
	startedAt, err := databaseNow(ctx, s.pool)
	if err != nil {
		return SiteStatus{}, err
	}

	if err := postBuildHook(ctx, hook); err != nil {
		s.auditSiteBuild(ctx, tenantID, triggeredBy, len(pending), err)
		return SiteStatus{}, fmt.Errorf("asking the website to rebuild: %w", err)
	}
	if err := setSiteBuilt(ctx, s.pool, tenantID, startedAt, triggeredBy); err != nil {
		return SiteStatus{}, err
	}
	s.auditSiteBuild(ctx, tenantID, triggeredBy, len(pending), nil)

	return s.SiteStatus(ctx, tenantID)
}

func postBuildHook(ctx context.Context, hook string) error {
	ctx, cancel := context.WithTimeout(ctx, siteBuildTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hook, strings.NewReader("{}"))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// Read and discard so the connection can be reused.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// The hook URL contains its own secret, so the URL is never echoed --
		// only what the far end said.
		return fmt.Errorf("build hook returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (s *Service) auditSiteBuild(ctx context.Context, tenantID uuid.UUID, triggeredBy string, changes int, runErr error) {
	meta := siteBuildMetadata{TriggeredBy: triggeredBy, Changes: changes}
	if runErr != nil {
		meta.Error = runErr.Error()
	}
	var actorID *uuid.UUID
	if c := auth.CallerFromContext(ctx); c != nil && c.UserID != uuid.Nil {
		id := c.UserID
		actorID = &id
	}
	_ = models.AppendAudit(ctx, s.pool, models.AuditEvent{
		TenantID:    tenantID,
		ActorUserID: actorID,
		Action:      actionSitePublished,
		Metadata:    meta,
	})
}
