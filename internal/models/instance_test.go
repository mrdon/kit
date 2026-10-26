package models

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mrdon/kit/internal/testdb"
)

// Only a live, strictly newer build supersedes. Self's stamp is now, so any
// other process sharing the test database was built earlier and stays out of
// the answer.
func TestNewerLiveInstance(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	built := time.Now().UTC().Truncate(time.Second)
	later := built.Add(time.Minute)
	window := 90 * time.Second

	beat := func(builtAt *time.Time) uuid.UUID {
		inst := Instance{ID: uuid.New(), Build: "test", BuiltAt: builtAt, Hostname: "test"}
		if err := HeartbeatInstance(ctx, pool, inst); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM kit_instances WHERE id = $1`, inst.ID) })
		return inst.ID
	}
	newer := func(self uuid.UUID, builtAt *time.Time) *Instance {
		got, err := NewerLiveInstance(ctx, pool, self, builtAt, window)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	self := beat(&built)
	if got := newer(self, &built); got != nil {
		t.Fatalf("superseded with no newer build: %+v", got)
	}

	beat(&built)
	if got := newer(self, &built); got != nil {
		t.Fatalf("an equal build superseded self: %+v", got)
	}

	next := beat(&later)
	if _, err := pool.Exec(ctx, `UPDATE kit_instances SET heartbeat_at = now() - interval '5 minutes' WHERE id = $1`, next); err != nil {
		t.Fatal(err)
	}
	if got := newer(self, &built); got != nil {
		t.Fatalf("a dead newer build superseded self: %+v", got)
	}

	if err := HeartbeatInstance(ctx, pool, Instance{ID: next, Build: "test", BuiltAt: &later, Hostname: "test"}); err != nil {
		t.Fatal(err)
	}
	if got := newer(self, &built); got == nil || got.ID != next {
		t.Fatalf("live newer build = %+v, want %s", got, next)
	}

	if got := newer(uuid.New(), nil); got == nil {
		t.Fatal("an unstamped build was not superseded by a stamped one")
	}
}
