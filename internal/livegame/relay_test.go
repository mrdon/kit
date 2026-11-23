package livegame

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// redisForTest returns a client against the local dev Redis, or skips. The
// relay is the piece that only matters at two web processes, so it is worth
// exercising against a real server rather than a fake.
func redisForTest(t *testing.T) *redis.Client {
	t.Helper()
	url := os.Getenv("REDIS_URL")
	if url == "" {
		url = "redis://localhost:6390"
	}
	opts, err := redis.ParseURL(url)
	if err != nil {
		t.Skipf("REDIS_URL not parseable: %v", err)
	}
	rdb := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		t.Skipf("redis not available at %s: %v", url, err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

// waitFor reads one snapshot with a timeout, so a failure says "nothing
// arrived" rather than hanging the suite.
func waitFor(t *testing.T, ch <-chan *testFrame, d time.Duration) *testFrame {
	t.Helper()
	select {
	case s := <-ch:
		return s
	case <-time.After(d):
		return nil
	}
}

// THE REASON THE RELAY EXISTS. At two web processes nginx round-robins the
// long-lived streams with no affinity, so a TV lands on process A while the
// host's click lands on B. Without this, B publishes to B's subscribers only
// and the TV freezes mid-question.
func TestRelayCarriesSnapshotsBetweenProcesses(t *testing.T) {
	rdb := redisForTest(t)
	// t.Context is cancelled when the test ends, which is what stops the
	// relay's PSUBSCRIBE goroutine.
	ctx := t.Context()

	gameID := uuid.New()

	// Two brokers standing in for two web processes, sharing one Redis.
	const prefix = "livegame-test:"
	brokerA := NewBroker[*testFrame]()
	relayA := NewRelay(rdb, brokerA, prefix)
	relayA.self = "process-a"
	brokerB := NewBroker[*testFrame]()
	relayB := NewRelay(rdb, brokerB, prefix)
	relayB.self = "process-b"
	relayB.Start(ctx)
	// PSUBSCRIBE is asynchronous; give it a moment to be live before
	// publishing, or the test races the subscription rather than the relay.
	time.Sleep(300 * time.Millisecond)

	ch, cancelSub := brokerB.Subscribe(gameID)
	defer cancelSub()

	snap := &testFrame{StateVersion: 42, Name: "brave-otter-lamp"}
	brokerA.Publish(gameID, snap)
	relayA.Publish(ctx, gameID, snap)

	got := waitFor(t, ch, 3*time.Second)
	if got == nil {
		t.Fatal("a snapshot published on one process never reached a subscriber on the other")
	}
	if got.StateVersion != 42 || got.Name != "brave-otter-lamp" {
		t.Fatalf("relayed snapshot = %+v", got)
	}
}

// A process must not re-deliver its own relayed message: the local broker
// already fanned it out, and doing it twice would repaint every client for
// nothing.
func TestRelayDoesNotRedeliverItsOwnMessage(t *testing.T) {
	rdb := redisForTest(t)
	ctx := t.Context()

	gameID := uuid.New()
	broker := NewBroker[*testFrame]()
	r := NewRelay(rdb, broker, "livegame-test-self:")
	r.Start(ctx)
	time.Sleep(300 * time.Millisecond)

	ch, cancelSub := broker.Subscribe(gameID)
	defer cancelSub()

	r.Publish(ctx, gameID, &testFrame{StateVersion: 7})

	if got := waitFor(t, ch, 1500*time.Millisecond); got != nil {
		t.Fatalf("the process re-delivered its own relayed message: %+v", got)
	}
}

// Degradation is preserved: with no Redis configured the relay is simply
// absent and fan-out is per-process, which is exactly correct at web=1.
// Nothing may require Redis to be up, so every method on the nil relay is a
// no-op rather than a panic.
func TestNilRelayIsANoOp(t *testing.T) {
	broker := NewBroker[*testFrame]()
	r := NewRelay(nil, broker, "livegame-test-nil:")
	if r != nil {
		t.Fatal("a nil redis client produced a relay")
	}
	r.Start(context.Background())
	r.Publish(context.Background(), uuid.New(), &testFrame{StateVersion: 1})

	gameID := uuid.New()
	ch, cancel := broker.Subscribe(gameID)
	defer cancel()
	broker.Publish(gameID, &testFrame{StateVersion: 1})
	if got := waitFor(t, ch, time.Second); got == nil {
		t.Fatal("local fan-out stopped working without redis")
	}
}
