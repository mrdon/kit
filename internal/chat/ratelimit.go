package chat

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// Per-user caps.
//
//   - ExecuteRateLimit protects Haiku spend and LLM concurrency against a
//     compromised session or runaway client loop.
//   - TranscribeRateLimit protects ffmpeg/whisper CPU: each call forks
//     two child processes and each transcription pins a core for seconds.
//   - MaxInFlight caps how many long-lived SSE connections a single user
//     can hold at once across both endpoints. Prevents parallel requests
//     from pinning N agent-loop goroutines + child processes around the
//     sliding-window limits.
const (
	ExecuteRateLimit       = 20
	ExecuteRateLimitWindow = 5 * time.Minute

	TranscribeRateLimit       = 30
	TranscribeRateLimitWindow = 5 * time.Minute

	MaxInFlightPerUser = 3
)

// ExecuteLimiter is a simple per-user sliding-window counter plus a
// per-user in-flight semaphore. Kept in process memory because the web
// frontend runs one instance today; if we scale out this moves to Redis.
type ExecuteLimiter struct {
	mu       sync.Mutex
	execute  map[uuid.UUID][]time.Time
	transcr  map[uuid.UUID][]time.Time
	inflight map[uuid.UUID]int
}

// NewExecuteLimiter returns a ready-to-use in-memory limiter.
func NewExecuteLimiter() *ExecuteLimiter {
	return &ExecuteLimiter{
		execute:  map[uuid.UUID][]time.Time{},
		transcr:  map[uuid.UUID][]time.Time{},
		inflight: map[uuid.UUID]int{},
	}
}

// Allow is the sliding-window check for chat/execute.
func (l *ExecuteLimiter) Allow(userID uuid.UUID) bool {
	return l.allow(userID, l.execute, ExecuteRateLimit, ExecuteRateLimitWindow)
}

// AllowTranscribe is the sliding-window check for chat/transcribe. It's
// a separate window because transcription is cheap per-request but
// expensive in CPU time — capping it prevents a client from queueing
// dozens of ffmpeg processes.
func (l *ExecuteLimiter) AllowTranscribe(userID uuid.UUID) bool {
	return l.allow(userID, l.transcr, TranscribeRateLimit, TranscribeRateLimitWindow)
}

func (l *ExecuteLimiter) allow(userID uuid.UUID, store map[uuid.UUID][]time.Time, cap int, window time.Duration) bool {
	now := time.Now()
	cutoff := now.Add(-window)

	l.mu.Lock()
	defer l.mu.Unlock()

	kept := store[userID][:0]
	for _, t := range store[userID] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= cap {
		store[userID] = kept
		return false
	}
	store[userID] = append(kept, now)
	return true
}

// Acquire reserves an in-flight slot for the user. Returns false if the
// user already has MaxInFlightPerUser active handlers; the caller
// should reject the request. Every successful Acquire must be paired
// with a Release.
func (l *ExecuteLimiter) Acquire(userID uuid.UUID) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inflight[userID] >= MaxInFlightPerUser {
		return false
	}
	l.inflight[userID]++
	return true
}

// Release returns an in-flight slot. Safe to call in a deferred call.
func (l *ExecuteLimiter) Release(userID uuid.UUID) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inflight[userID] > 0 {
		l.inflight[userID]--
	}
	if l.inflight[userID] == 0 {
		// Free the zero entries so the map stays bounded under churn.
		delete(l.inflight, userID)
	}
}
