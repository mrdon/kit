package livegame

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// The loop sweeps once at boot (healing what came due while the process was
// down), then on every wake -- a publish is how it learns a deadline moved.
func TestSweeperSweepsAtBootAndOnWake(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	wake := make(chan struct{}, 1)
	var sweeps atomic.Int32
	Sweeper{
		Name:  "test",
		Wake:  wake,
		Next:  func(context.Context) (time.Duration, bool, error) { return 0, false, nil }, // nothing due: park
		Sweep: func(context.Context) { sweeps.Add(1) },
	}.Start(ctx)

	waitUntil(t, func() bool { return sweeps.Load() == 1 }, "the boot sweep")
	wake <- struct{}{}
	waitUntil(t, func() bool { return sweeps.Load() == 2 }, "a sweep on wake")
}

// With a deadline due, the loop wakes itself for it -- no publish needed.
func TestSweeperWakesForTheNextDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var sweeps atomic.Int32
	Sweeper{
		Name:  "test",
		Wake:  make(chan struct{}),
		Next:  func(context.Context) (time.Duration, bool, error) { return time.Millisecond, true, nil },
		Sweep: func(context.Context) { sweeps.Add(1) },
	}.Start(ctx)
	// MinWait floors each sleep, so three sweeps is about half a second.
	waitUntil(t, func() bool { return sweeps.Load() >= 3 }, "sweeps on the deadline timer")
}

func waitUntil(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
