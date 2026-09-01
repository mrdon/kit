package trivia

import (
	"sync"

	"github.com/google/uuid"
)

// Broker fans a game's snapshots out to everything watching it: the TV, the
// host console, and up to twenty phones.
//
// Every frame is a FULL snapshot rather than a delta. That decision pays for
// itself three times: a phone joining mid-game renders correctly with no
// separate bootstrap path to drift from the live one, a reconnect after a
// dropped wifi association needs no replay, and ordering bugs stop existing
// because there is no order to get wrong. A snapshot is 3-6 KB, which at
// twenty-one clients and a handful of frames per question is nothing.
type Broker struct {
	mu   sync.Mutex
	subs map[uuid.UUID]map[*subscriber]struct{}
}

// subscriber is a latest-wins mailbox of capacity one, not a buffered channel
// with a drop policy.
//
// Because every frame is a complete snapshot, a subscriber that has fallen
// behind does not want the frames it missed -- it wants the newest one. So
// offer() drains whatever is sitting in the box and puts the new snapshot
// there instead, and "slow consumer" stops being a failure class: a publisher
// never blocks, and no client is ever dropped for being slow.
type subscriber struct {
	ch chan *Snapshot
}

// NewBroker returns an empty broker.
func NewBroker() *Broker {
	return &Broker{subs: map[uuid.UUID]map[*subscriber]struct{}{}}
}

// Subscribe registers a watcher for one game and returns its channel plus a
// cancel func. The cancel is idempotent and MUST be deferred by the caller:
// it is what keeps the map from growing for the life of the process.
func (b *Broker) Subscribe(gameID uuid.UUID) (<-chan *Snapshot, func()) {
	s := &subscriber{ch: make(chan *Snapshot, 1)}
	b.mu.Lock()
	if b.subs[gameID] == nil {
		b.subs[gameID] = map[*subscriber]struct{}{}
	}
	b.subs[gameID][s] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	return s.ch, func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			set := b.subs[gameID]
			if set == nil {
				return
			}
			delete(set, s)
			// Emptying a game's set deletes the map entry, so a venue that
			// has run four hundred quiz nights does not carry four hundred
			// empty maps.
			if len(set) == 0 {
				delete(b.subs, gameID)
			}
			close(s.ch)
		})
	}
}

// Publish hands a snapshot to every local watcher of a game. It never blocks
// and never drops a client.
//
// The relay to other web processes is layered on top of this in
// broker_redis.go; handlers only ever talk to the local broker.
func (b *Broker) Publish(gameID uuid.UUID, snap *Snapshot) {
	b.mu.Lock()
	subs := make([]*subscriber, 0, len(b.subs[gameID]))
	for s := range b.subs[gameID] {
		subs = append(subs, s)
	}
	b.mu.Unlock()
	for _, s := range subs {
		b.offer(s, snap)
	}
}

// offer replaces whatever is in the mailbox with the newest snapshot.
//
// The lock is held so a concurrent cancel cannot close the channel between
// the drain and the send -- closing a channel a publisher is about to write
// to is the one way this design could panic, and it is the case the race
// tests exercise.
func (b *Broker) offer(s *subscriber, snap *Snapshot) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, live := b.subs[snap.GameID][s]; !live {
		return // cancelled while we were assembling the fan-out list
	}
	select {
	case <-s.ch: // discard the stale frame nobody read
	default:
	}
	select {
	case s.ch <- snap:
	default: // a reader took the slot between the drain and here; theirs is newer
	}
}

// SubscriberCount reports live watchers for a game. Test and diagnostics
// only; nothing in the game's behaviour depends on it.
func (b *Broker) SubscriberCount(gameID uuid.UUID) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs[gameID])
}

// GameCount reports how many games have at least one watcher, which is the
// number that must return to zero when a night ends.
func (b *Broker) GameCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}
