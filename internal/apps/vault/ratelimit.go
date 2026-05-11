package vault

import (
	"sync"
	"time"
)

// unlockLimiter is the per-IP token bucket used by /api/vault/unlock.
// In the shared-password model this is the *only* rate-limit on unlock —
// there is no per-tenant counter, because one global counter combined
// with one shared password would create a self-DoS griefing vector (any
// ex-employee who still knows the old password could lock out the whole
// team by spamming wrong passwords). Per-IP catches online brute force;
// PBKDF2 600k iter is the offline-brute-force defense.
type unlockLimiter struct {
	mu       sync.Mutex
	buckets  map[string]*tokenBucket
	capacity int
	refill   time.Duration
}

type tokenBucket struct {
	tokens  int
	updated time.Time
}

func newUnlockLimiter(capacity int, refill time.Duration) unlockLimiter {
	return unlockLimiter{
		buckets:  make(map[string]*tokenBucket),
		capacity: capacity,
		refill:   refill,
	}
}

// allow returns true if the bucket for `key` has at least one token,
// debiting one. Refill is full-capacity per refill interval since last
// activity. The map is capped at 10k entries; once full, ~half are
// evicted on next miss (random-order, not LRU — Go map iteration is
// randomized; good enough for v1).
func (u *unlockLimiter) allow(key string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	now := time.Now()
	b, ok := u.buckets[key]
	if !ok {
		b = &tokenBucket{tokens: u.capacity, updated: now}
		if len(u.buckets) > 10_000 {
			for k := range u.buckets {
				delete(u.buckets, k)
				if len(u.buckets) <= 5_000 {
					break
				}
			}
		}
		u.buckets[key] = b
	}
	elapsed := now.Sub(b.updated)
	gain := int(elapsed/u.refill) * u.capacity
	if gain > 0 {
		b.tokens += gain
		if b.tokens > u.capacity {
			b.tokens = u.capacity
		}
		b.updated = now
	}
	if b.tokens <= 0 {
		return false
	}
	b.tokens--
	return true
}
