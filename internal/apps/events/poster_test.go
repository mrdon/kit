package events

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// The poster endpoint is a build-time API behind the feed token, and behind
// that IsPubliclyVisible decides what a token holder may actually fetch. These
// pin the feed half: it only ever advertises a poster for an event the public
// may see, and never invents a URL for an event that has no poster (which
// would render as a broken image on the site).
func TestFeedPosterURLOnlyForPublicEventsWithAPoster(t *testing.T) {
	sf := newSyncFixture(t)
	const base = "https://kit.example.com/gravity"
	hero := sf.attachment(t)

	// Published + public + poster: the only combination that gets a URL.
	live := sf.create(t, CreateParams{Title: "Bike Night", Visibility: VisibilityPublic})
	sf.publish(t, live)
	sf.setHero(t, live, &hero)

	// Published + public, no poster.
	bare := sf.create(t, CreateParams{Title: "Quiz Night", Visibility: VisibilityPublic})
	sf.publish(t, bare)

	// Published + PRIVATE, with a poster. Must not appear at all.
	party := sf.create(t, CreateParams{Title: "Sarah's 40th", Visibility: VisibilityPrivate})
	sf.publish(t, party)
	sf.setHero(t, party, &hero)

	f, err := sf.svc.BuildFeed(sf.ctx, sf.tenant.ID, base)
	if err != nil {
		t.Fatalf("BuildFeed: %v", err)
	}

	got := map[string]string{}
	for _, e := range f.Events {
		got[e.Title] = e.ImageURL
	}
	if _, leaked := got["Sarah's 40th"]; leaked {
		t.Fatal("a private event reached the feed at all")
	}
	if want := base + "/events/" + live.Slug + "/poster"; got["Bike Night"] != want {
		t.Errorf("poster URL = %q, want %q", got["Bike Night"], want)
	}
	if got["Quiz Night"] != "" {
		t.Errorf("event with no poster advertised one: %q", got["Quiz Night"])
	}
}

// Without a base URL there is no host to build against, so no poster field --
// rather than a relative path the consumer would resolve against its own site.
func TestFeedOmitsPosterWithoutBaseURL(t *testing.T) {
	sf := newSyncFixture(t)
	hero := sf.attachment(t)
	live := sf.create(t, CreateParams{Title: "Bike Night", Visibility: VisibilityPublic})
	sf.publish(t, live)
	sf.setHero(t, live, &hero)

	f, err := sf.svc.BuildFeed(sf.ctx, sf.tenant.ID, "")
	if err != nil {
		t.Fatalf("BuildFeed: %v", err)
	}
	for _, e := range f.Events {
		if e.ImageURL != "" {
			t.Errorf("poster URL emitted with no base: %q", e.ImageURL)
		}
	}
}

// The upload allowlist exists because the bytes are served back to a browser.
// An SVG can carry script; letting one through would be stored XSS on the
// brewery's own domain.
func TestPosterMIMEAllowlistRejectsScriptableTypes(t *testing.T) {
	for _, bad := range []string{"image/svg+xml", "text/html", "application/pdf", "text/xml"} {
		if _, ok := allowedPosterMIME[bad]; ok {
			t.Errorf("%s is allowed as a poster type", bad)
		}
	}
	for _, good := range []string{"image/jpeg", "image/png", "image/webp", "image/gif"} {
		if _, ok := allowedPosterMIME[good]; !ok {
			t.Errorf("%s should be an allowed poster type", good)
		}
	}
}

// PosterURL is shared by the feed and any future consumer, so its shape is
// pinned -- including that it tolerates a trailing slash on the base.
func TestPosterURLShape(t *testing.T) {
	want := "https://kit.example.com/gravity/events/bike-night/poster"
	for _, base := range []string{"https://kit.example.com", "https://kit.example.com/"} {
		if got := PosterURL(base, "gravity", "bike-night"); got != want {
			t.Errorf("PosterURL(%q) = %q, want %q", base, got, want)
		}
	}
	if !strings.HasSuffix(PosterURL("x", "t", "e"), "/poster") {
		t.Error("poster URL must end in /poster")
	}
}

// attachment inserts a bare attachment row. app_events.hero_attachment_id has
// a real foreign key, so a synthetic uuid will not do.
func (sf *syncFixture) attachment(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := sf.svc.pool.Exec(sf.ctx,
		`INSERT INTO attachments (id, tenant_id, filename, mime, size, data)
		 VALUES ($1, $2, 'poster.jpg', 'image/jpeg', 3, $3)`,
		id, sf.tenant.ID, []byte{1, 2, 3})
	if err != nil {
		t.Fatalf("seeding attachment: %v", err)
	}
	return id
}

// setHero attaches a poster. The attachment row itself is irrelevant to these
// tests -- only whether the id is set decides what the feed advertises.
func (sf *syncFixture) setHero(t *testing.T, e *Event, id *uuid.UUID) {
	t.Helper()
	if _, err := sf.svc.Update(sf.ctx, sf.tenant.ID, e.ID, UpdateParams{HeroAttachmentID: id}); err != nil {
		t.Fatalf("setting hero: %v", err)
	}
}
