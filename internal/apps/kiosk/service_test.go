package kiosk

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/testdb"
)

type fixture struct {
	pool   *pgxpool.Pool
	svc    *Service
	tenant *models.Tenant
	ctx    context.Context
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := testdb.Open(t)
	ctx := context.Background()

	teamID := "T_kiosk_test_" + uuid.NewString()
	slug := models.SanitizeSlug("kiosk-test-"+uuid.NewString(), teamID)
	tenant, err := models.UpsertTenant(ctx, pool, teamID, "kiosk-test", "encrypted-placeholder", slug, nil, nil)
	if err != nil {
		t.Fatalf("creating tenant: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, "DELETE FROM tenants WHERE id = $1", tenant.ID) })

	return &fixture{pool: pool, svc: NewService(pool), tenant: tenant, ctx: ctx}
}

func TestCreateDerivesKeyFromName(t *testing.T) {
	f := newFixture(t)
	b, err := f.svc.Create(f.ctx, f.tenant.ID, BoardInput{Name: "Lobby TV", URL: "https://example.com/a"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if b.Key != "lobby-tv" {
		t.Fatalf("key = %q, want lobby-tv", b.Key)
	}
}

func TestCreateRejectsBadInput(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name string
		in   BoardInput
		want error
	}{
		{"no name", BoardInput{URL: "https://example.com"}, ErrNameRequired},
		{"name with no slug-able characters", BoardInput{Name: "!!!"}, ErrKeyInvalid},
		{"explicit key with a slash", BoardInput{Name: "Lobby", Key: "a/b"}, ErrKeyInvalid},
		{"relative url", BoardInput{Name: "Lobby", URL: "/dashboard"}, ErrURLInvalid},
		{"javascript scheme", BoardInput{Name: "Lobby", URL: "javascript:alert(1)"}, ErrURLInvalid},
		{"file scheme", BoardInput{Name: "Lobby", URL: "file:///etc/passwd"}, ErrURLInvalid},
		{"scheme with no host", BoardInput{Name: "Lobby", URL: "https://"}, ErrURLInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.svc.Create(f.ctx, f.tenant.ID, tc.in); !errors.Is(err, tc.want) {
				t.Fatalf("Create: err = %v, want %v", err, tc.want)
			}
		})
	}
}

// A board with no URL is a supported state — a screen provisioned before
// anyone decided what it shows — so an empty URL must not be rejected.
func TestCreateAllowsEmptyURL(t *testing.T) {
	f := newFixture(t)
	b, err := f.svc.Create(f.ctx, f.tenant.ID, BoardInput{Name: "Unassigned"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if b.URL != "" {
		t.Fatalf("URL = %q, want empty", b.URL)
	}
}

func TestDuplicateKeyConflicts(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.Create(f.ctx, f.tenant.ID, BoardInput{Name: "Lobby", URL: "https://example.com"}); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := f.svc.Create(f.ctx, f.tenant.ID, BoardInput{Name: "Lobby", URL: "https://example.com/other"})
	if !errors.Is(err, ErrKeyTaken) {
		t.Fatalf("second Create: err = %v, want ErrKeyTaken", err)
	}
}

// The key is unique per tenant, not globally: two workspaces both naming a
// screen "lobby" is the expected case, not a collision.
func TestSameKeyAcrossTenants(t *testing.T) {
	a, b := newFixture(t), newFixture(t)
	if _, err := a.svc.Create(a.ctx, a.tenant.ID, BoardInput{Name: "Lobby", URL: "https://a.example.com"}); err != nil {
		t.Fatalf("tenant A Create: %v", err)
	}
	if _, err := b.svc.Create(b.ctx, b.tenant.ID, BoardInput{Name: "Lobby", URL: "https://b.example.com"}); err != nil {
		t.Fatalf("tenant B Create: %v", err)
	}
}

// Resolve is the public endpoint's only lookup, and it is the tenant
// isolation boundary for an unauthenticated route: one tenant's key must
// never resolve against another tenant's board.
func TestResolveIsTenantScoped(t *testing.T) {
	a, b := newFixture(t), newFixture(t)
	if _, err := a.svc.Create(a.ctx, a.tenant.ID, BoardInput{Name: "Lobby", URL: "https://a.example.com"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := b.svc.Resolve(b.ctx, b.tenant.ID, "lobby"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant Resolve: err = %v, want ErrNotFound", err)
	}
	got, err := a.svc.Resolve(a.ctx, a.tenant.ID, "lobby")
	if err != nil {
		t.Fatalf("same-tenant Resolve: %v", err)
	}
	if got.URL != "https://a.example.com" {
		t.Fatalf("URL = %q", got.URL)
	}
}

func TestUpdateChangesURL(t *testing.T) {
	f := newFixture(t)
	b, err := f.svc.Create(f.ctx, f.tenant.ID, BoardInput{Name: "Lobby", URL: "https://old.example.com"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	updated, err := f.svc.Update(f.ctx, f.tenant.ID, b.ID, BoardInput{
		Key: b.Key, Name: b.Name, URL: "https://new.example.com",
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.URL != "https://new.example.com" {
		t.Fatalf("URL = %q", updated.URL)
	}
	resolved, err := f.svc.Resolve(f.ctx, f.tenant.ID, "lobby")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.URL != "https://new.example.com" {
		t.Fatalf("resolved URL = %q, want the updated one", resolved.URL)
	}
}

// Updating another tenant's board by id must not work even though the id is
// well-formed and the row exists.
func TestUpdateIsTenantScoped(t *testing.T) {
	a, b := newFixture(t), newFixture(t)
	board, err := a.svc.Create(a.ctx, a.tenant.ID, BoardInput{Name: "Lobby", URL: "https://a.example.com"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err = b.svc.Update(b.ctx, b.tenant.ID, board.ID, BoardInput{Name: "Hijacked", URL: "https://evil.example.com"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant Update: err = %v, want ErrNotFound", err)
	}
}

func TestDeleteIsTenantScoped(t *testing.T) {
	a, b := newFixture(t), newFixture(t)
	board, err := a.svc.Create(a.ctx, a.tenant.ID, BoardInput{Name: "Lobby", URL: "https://a.example.com"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := b.svc.Delete(b.ctx, b.tenant.ID, board.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant Delete: err = %v, want ErrNotFound", err)
	}
	if err := a.svc.Delete(a.ctx, a.tenant.ID, board.ID); err != nil {
		t.Fatalf("owner Delete: %v", err)
	}
}

func TestTouchRecordsLastSeen(t *testing.T) {
	f := newFixture(t)
	b, err := f.svc.Create(f.ctx, f.tenant.ID, BoardInput{Name: "Lobby", URL: "https://example.com"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if b.LastSeenAt != nil {
		t.Fatal("a board nothing has polled should have no last_seen_at")
	}
	if err := TouchBoardSeen(f.ctx, f.pool, f.tenant.ID, b.ID); err != nil {
		t.Fatalf("TouchBoardSeen: %v", err)
	}
	after, err := f.svc.Resolve(f.ctx, f.tenant.ID, "lobby")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if after.LastSeenAt == nil {
		t.Fatal("last_seen_at not recorded after a poll")
	}
}

func TestSlugifyKey(t *testing.T) {
	cases := map[string]string{
		"Lobby TV":              "lobby-tv",
		"  Brewhouse  Screen  ": "brewhouse-screen",
		"Taproom #2":            "taproom-2",
		"a---b":                 "a-b",
		"!!!":                   "",
	}
	for in, want := range cases {
		if got := SlugifyKey(in); got != want {
			t.Errorf("SlugifyKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPublicPath(t *testing.T) {
	if got := PublicPath("acme", "lobby"); got != "/acme/kiosk/lobby" {
		t.Fatalf("PublicPath = %q", got)
	}
}
