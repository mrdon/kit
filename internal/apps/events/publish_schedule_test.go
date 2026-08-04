package events

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

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
