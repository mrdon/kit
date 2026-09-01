package trivia

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func snapFor(gameID uuid.UUID, version int64) *Snapshot {
	return &Snapshot{GameID: gameID, StateVersion: version, PublisherID: processID}
}

func TestBrokerDeliversToEverySubscriber(t *testing.T) {
	b := NewBroker()
	game := uuid.New()
	chA, cancelA := b.Subscribe(game)
	defer cancelA()
	chB, cancelB := b.Subscribe(game)
	defer cancelB()

	b.Publish(game, snapFor(game, 7))

	for i, ch := range []<-chan *Snapshot{chA, chB} {
		select {
		case got := <-ch:
			if got.StateVersion != 7 {
				t.Fatalf("subscriber %d got version %d", i, got.StateVersion)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d received nothing", i)
		}
	}
}

// A snapshot for one game must not reach a watcher of another. Two quiz
// nights in one workspace on the same evening is not exotic.
func TestBrokerDoesNotCrossGames(t *testing.T) {
	b := NewBroker()
	a, other := uuid.New(), uuid.New()
	ch, cancel := b.Subscribe(a)
	defer cancel()

	b.Publish(other, snapFor(other, 1))
	select {
	case got := <-ch:
		t.Fatalf("received another game's snapshot: %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
}

// The mailbox is latest-wins. Because every frame is a full snapshot, a
// subscriber that is behind does not want the frames it missed — it wants the
// newest one — so "slow consumer" stops being a failure class.
func TestBrokerCoalescesToTheNewestSnapshot(t *testing.T) {
	b := NewBroker()
	game := uuid.New()
	ch, cancel := b.Subscribe(game)
	defer cancel()

	for v := int64(1); v <= 50; v++ {
		b.Publish(game, snapFor(game, v))
	}
	select {
	case got := <-ch:
		if got.StateVersion != 50 {
			t.Fatalf("got version %d, want the newest (50)", got.StateVersion)
		}
	case <-time.After(time.Second):
		t.Fatal("nothing delivered")
	}
	select {
	case got := <-ch:
		t.Fatalf("a second frame was queued (version %d) — the mailbox is not capacity 1", got.StateVersion)
	case <-time.After(50 * time.Millisecond):
	}
}

// The property the whole design rests on: a phone that has stopped reading
// must never be able to block the host's click.
func TestBrokerPublisherNeverBlocksOnASilentSubscriber(t *testing.T) {
	b := NewBroker()
	game := uuid.New()
	_, cancel := b.Subscribe(game) // deliberately never read
	defer cancel()

	done := make(chan struct{})
	go func() {
		for v := int64(1); v <= 10000; v++ {
			b.Publish(game, snapFor(game, v))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("publisher blocked on a subscriber that never read")
	}
}

// Cancel is idempotent and emptying a game's set removes the map entry, so a
// venue that has run four hundred quiz nights does not carry four hundred
// empty maps.
func TestBrokerCleansUpAfterItself(t *testing.T) {
	b := NewBroker()
	game := uuid.New()
	_, cancel1 := b.Subscribe(game)
	_, cancel2 := b.Subscribe(game)
	if got := b.SubscriberCount(game); got != 2 {
		t.Fatalf("subscribers = %d, want 2", got)
	}
	cancel1()
	cancel1() // must be safe twice
	if got := b.SubscriberCount(game); got != 1 {
		t.Fatalf("subscribers = %d after one cancel, want 1", got)
	}
	cancel2()
	if got := b.GameCount(); got != 0 {
		t.Fatalf("%d games still tracked with no subscribers", got)
	}
}

// Unsubscribing while a publish is in flight is the one case that could panic
// on send-to-closed-channel. Run under -race.
func TestBrokerUnsubscribeRacingAPublish(t *testing.T) {
	b := NewBroker()
	game := uuid.New()

	var wg sync.WaitGroup
	for range 40 {
		wg.Add(2)
		ch, cancel := b.Subscribe(game)
		go func() {
			defer wg.Done()
			for v := int64(1); v <= 100; v++ {
				b.Publish(game, snapFor(game, v))
			}
		}()
		go func() {
			defer wg.Done()
			<-ch
			cancel()
			// Drain whatever is left so the closed channel is read from too.
			for range ch {
			}
		}()
	}
	wg.Wait()
}
