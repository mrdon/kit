package events

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mrdon/kit/internal/models"
)

// buildHookRecorder stands in for Netlify, counting how many builds we
// actually asked for.
func buildHookRecorder(t *testing.T) (url string, calls *atomic.Int64) {
	t.Helper()
	var n atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &n
}

// setBuildHook points the fixture's tenant at a hook URL.
func (sf *syncFixture) setBuildHook(t *testing.T, url string) {
	t.Helper()
	next := sf.sett
	next.SiteBuildHookURL = url
	saved, err := upsertSettings(sf.ctx, sf.pool, next)
	if err != nil {
		t.Fatalf("saving build hook: %v", err)
	}
	sf.sett = saved
}

// TestNightlyPublishSkipsWhenNothingChanged is the whole point of the
// pending check. Netlify bills build minutes and most nights nothing has
// changed, so a blind nightly build would spend them producing a
// byte-identical site.
func TestNightlyPublishSkipsWhenNothingChanged(t *testing.T) {
	sf := newSyncFixture(t)
	hookURL, calls := buildHookRecorder(t)
	sf.setBuildHook(t, hookURL)

	job := models.Job{TenantID: sf.tenant.ID}
	if err := sf.app.publishSiteIfChanged(sf.ctx, job); err != nil {
		t.Fatalf("publishSiteIfChanged: %v", err)
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("hook fired %d times with nothing pending, want 0", got)
	}
}

// TestNightlyPublishBuildsWhenSomethingChanged is the other half: a real
// pending change must actually reach the site, and must not still be pending
// afterwards.
func TestNightlyPublishBuildsWhenSomethingChanged(t *testing.T) {
	sf := newSyncFixture(t)
	hookURL, calls := buildHookRecorder(t)
	sf.setBuildHook(t, hookURL)

	e := sf.create(t, CreateParams{Title: "Trivia Night", Visibility: VisibilityPublic})
	sf.publish(t, e)

	job := models.Job{TenantID: sf.tenant.ID}
	if err := sf.app.publishSiteIfChanged(sf.ctx, job); err != nil {
		t.Fatalf("publishSiteIfChanged: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("hook fired %d times with a pending change, want 1", got)
	}

	// A second run the following night has nothing left to do.
	if err := sf.app.publishSiteIfChanged(sf.ctx, job); err != nil {
		t.Fatalf("second publishSiteIfChanged: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("hook fired %d times total; the second run should have skipped", got)
	}
}

// TestBuildHookConfiguredGatesTheRow keeps the nightly job off tenants with
// nowhere to publish to, which would otherwise fail every single night.
func TestBuildHookConfiguredGatesTheRow(t *testing.T) {
	sf := newSyncFixture(t)

	if sf.app.buildHookConfigured(sf.ctx, sf.tenant.ID) {
		t.Fatal("no hook set, but the tenant qualifies for the nightly publish")
	}

	hookURL, _ := buildHookRecorder(t)
	sf.setBuildHook(t, hookURL)

	if !sf.app.buildHookConfigured(sf.ctx, sf.tenant.ID) {
		t.Fatal("hook is set, but the tenant does not qualify")
	}
}

// The build mark and the change timestamps it is compared against must come
// from the SAME clock.
//
// This was a real bug and an intermittent CI failure. site_built_at was
// stamped with the app process's time.Now(), while audit_events.created_at is
// stamped by the database's now(). The two are compared with ">", and the gap
// between an edit and the build that follows it is well under a millisecond —
// so a database whose clock ran even slightly ahead left an
// already-published change reading as still pending. Forever: the nightly job
// would rebuild the site every night and never converge.
//
// Asserting "nothing is pending" alone only catches it when the skew happens
// to be positive during the test run, which is exactly why it presented as a
// flake. This asserts the invariant instead.
func TestSiteBuildMarkComesFromTheDatabaseClock(t *testing.T) {
	sf := newSyncFixture(t)
	hookURL, _ := buildHookRecorder(t)
	sf.setBuildHook(t, hookURL)

	e := sf.create(t, CreateParams{Title: "Trivia Night", Visibility: VisibilityPublic})
	sf.publish(t, e)

	if _, err := sf.app.svc.PublishSite(sf.ctx, sf.tenant.ID, "test"); err != nil {
		t.Fatalf("PublishSite: %v", err)
	}

	// The mark must be at or after every change it claims to have published,
	// measured on the database's own clock.
	var newestChange, builtAt time.Time
	if err := sf.pool.QueryRow(sf.ctx, `
		SELECT max(a.created_at),
		       (SELECT site_built_at FROM app_event_settings WHERE tenant_id = $1)
		  FROM audit_events a
		 WHERE a.tenant_id = $1
		   AND a.action LIKE 'events.event_%'
		   AND a.metadata->>'affects_site' = 'true'`, sf.tenant.ID).
		Scan(&newestChange, &builtAt); err != nil {
		t.Fatalf("reading timestamps: %v", err)
	}
	if builtAt.Before(newestChange) {
		t.Fatalf("the build mark (%s) predates the change it published (%s) by %v — "+
			"the two came from different clocks",
			builtAt, newestChange, newestChange.Sub(builtAt))
	}

	// And the consequence: nothing is left pending.
	status, err := sf.app.svc.SiteStatus(sf.ctx, sf.tenant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Pending) != 0 {
		t.Fatalf("%d changes still pending right after a publish", len(status.Pending))
	}
}
