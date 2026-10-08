package devices

import (
	"sync"
	"time"
)

// Pairing is the one unauthenticated write in this app, so it is limited
// per client IP: a stranger on the venue wifi can open /pair a few times,
// not fill the admin's list with hundreds of decoys. In process memory,
// like the chat limiter; the deployment is one web process.
const (
	pairingsPerIP     = 10
	pairingsPerWindow = 10 * time.Minute
)

type ipLimiter struct {
	mu   sync.Mutex
	seen map[string][]time.Time
}

func newIPLimiter() *ipLimiter {
	return &ipLimiter{seen: map[string][]time.Time{}}
}

// allow records an attempt from ip and reports whether it is within the
// window's budget. An unknown ip ("") shares one bucket, which is the
// conservative choice.
func (l *ipLimiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := now.Add(-pairingsPerWindow)
	kept := l.seen[ip][:0]
	for _, t := range l.seen[ip] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= pairingsPerIP {
		l.seen[ip] = kept
		return false
	}
	l.seen[ip] = append(kept, now)
	// Keep the map from growing with every IP that ever tried once.
	if len(l.seen) > 10000 {
		for k, ts := range l.seen {
			if len(ts) == 0 || !ts[len(ts)-1].After(cutoff) {
				delete(l.seen, k)
			}
		}
	}
	return true
}

// refund forgets the most recent attempt from ip. Called when an attempt
// succeeded, so a legitimate client that retries is never counted against
// the budget meant for guessing.
func (l *ipLimiter) refund(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if ts := l.seen[ip]; len(ts) > 0 {
		l.seen[ip] = ts[:len(ts)-1]
	}
}
