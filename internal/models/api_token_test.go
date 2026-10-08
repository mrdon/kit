package models

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/testdb"
)

// The reaper deletes only rows expired for longer than the grace period,
// and only for the tenant whose job is running.
func TestDeleteExpiredAPITokens(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	tenantID, userID := testTenantUser(t, ctx, pool)
	otherTenant, otherUser := testTenantUser(t, ctx, pool)

	grace := 24 * time.Hour
	now := time.Now()
	rows := map[string]struct {
		tenant, user uuid.UUID
		expires      time.Time
		wantKept     bool
	}{
		"live":               {tenantID, userID, now.Add(time.Hour), true},
		"expired in grace":   {tenantID, userID, now.Add(-time.Hour), true},
		"expired past grace": {tenantID, userID, now.Add(-2 * grace), false},
		"other tenant stale": {otherTenant, otherUser, now.Add(-2 * grace), true},
	}
	hashes := map[string]string{}
	for name, r := range rows {
		hashes[name] = "hash-" + uuid.NewString()
		if err := CreateAPIToken(ctx, pool, r.tenant, r.user, hashes[name], r.expires); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}

	n, err := DeleteExpiredAPITokens(ctx, pool, tenantID, grace)
	if err != nil {
		t.Fatalf("DeleteExpiredAPITokens: %v", err)
	}
	if n != 1 {
		t.Fatalf("deleted %d rows, want 1", n)
	}
	for name, r := range rows {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM api_tokens WHERE token_hash = $1)`, hashes[name]).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists != r.wantKept {
			t.Errorf("%s: exists = %v, want %v", name, exists, r.wantKept)
		}
	}
}
