package posters

import (
	"testing"
	"time"

	"github.com/mrdon/kit/internal/models"
	"github.com/mrdon/kit/internal/services"
)

func TestStockNeedsASwitchAndAKey(t *testing.T) {
	f := newFixture(t)
	caller := &services.Caller{TenantID: f.tenant.ID, UserID: f.user.ID}
	if f.app.stockAllowed(f.ctx, caller) {
		t.Fatal("stock allowed with nothing configured")
	}
	if _, err := upsertSettings(f.ctx, f.pool, Settings{TenantID: f.tenant.ID, AllowStockPhotos: true, LogoMap: map[string]LogoFile{}}); err != nil {
		t.Fatal(err)
	}
	if f.app.stockAllowed(f.ctx, caller) {
		t.Fatal("stock allowed with the switch on but no key anywhere")
	}
	f.app.serverPixabayKey = "server-key"
	if !f.app.stockAllowed(f.ctx, caller) {
		t.Fatal("stock not allowed with the switch on and a server key")
	}
	if key, _ := f.app.pixabayKey(f.ctx, f.tenant.ID); key != "server-key" {
		t.Fatalf("key = %q, want the server fallback", key)
	}

	// Connecting Pixabay on the Integrations page stores the key encrypted;
	// the workspace's own key wins over the server's.
	pending, err := models.CreatePendingIntegration(f.ctx, f.pool, f.tenant.ID, f.user.ID, stockProvider, stockAuthType, nil, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := f.app.enc.Encrypt("workspace-key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := models.CompletePendingIntegration(f.ctx, f.pool, f.tenant.ID, pending.ID, nil, &enc, nil, nil); err != nil {
		t.Fatal(err)
	}
	if key, err := f.app.pixabayKey(f.ctx, f.tenant.ID); err != nil || key != "workspace-key" {
		t.Fatalf("key = %q (%v), want the workspace key", key, err)
	}
	f.app.serverPixabayKey = ""
	if !f.app.stockAllowed(f.ctx, caller) {
		t.Fatal("stock not allowed with a connected key and no server key")
	}
}
