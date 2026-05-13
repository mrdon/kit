package widget

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// Limiter is an in-memory token-bucket per widget-token id. Lossy on
// process restart, which is acceptable for v1: the cost of a few
// extra requests after a deploy is far below the cost of a Redis
// dependency for this surface.
//
// Defaults: capacity 60 requests, refill rate 1/min — i.e. 60
// requests/hour steady-state with a 60-request burst. Suitable for a
// single visitor having a brisk back-and-forth without throttling, but
// stops automated abuse.
type Limiter struct {
	mu       sync.Mutex
	buckets  map[uuid.UUID]*bucket
	capacity float64
	refill   float64 // tokens per second
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewLimiter returns a limiter with the default 60/hour budget.
func NewLimiter() *Limiter {
	return &Limiter{
		buckets:  make(map[uuid.UUID]*bucket),
		capacity: 60,
		refill:   1.0 / 60.0, // 60 tokens per 3600s = 1 per 60s
	}
}

// Allow reports whether a single request from the given token is
// permitted right now. Consumes one token on success.
func (l *Limiter) Allow(tokenID uuid.UUID) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b, ok := l.buckets[tokenID]
	if !ok {
		l.buckets[tokenID] = &bucket{tokens: l.capacity - 1, last: now}
		return true
	}
	elapsed := now.Sub(b.last).Seconds()
	b.tokens += elapsed * l.refill
	if b.tokens > l.capacity {
		b.tokens = l.capacity
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
