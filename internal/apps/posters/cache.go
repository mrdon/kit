package posters

import (
	"context"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Renders are not stored: options and edit previews live in a cache for a
// day, keyed by version id and format, and re-render from source on a
// miss. Redis when Kit has it (so two web containers share), else memory.

const renderTTL = 24 * time.Hour

type renderCache struct {
	rdb *redis.Client
	mu  sync.Mutex
	mem map[string]memEntry
}

type memEntry struct {
	png []byte
	at  time.Time
}

func newRenderCache(rdb *redis.Client) *renderCache {
	return &renderCache{rdb: rdb, mem: map[string]memEntry{}}
}

func renderKey(versionID, format string) string { return "posters:render:" + versionID + ":" + format }

func (c *renderCache) get(ctx context.Context, versionID, format string) []byte {
	key := renderKey(versionID, format)
	if c.rdb != nil {
		b, err := c.rdb.Get(ctx, key).Bytes()
		if err == nil {
			return b
		}
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.mem[key]
	if !ok || time.Since(e.at) > renderTTL {
		delete(c.mem, key)
		return nil
	}
	return e.png
}

func (c *renderCache) put(ctx context.Context, versionID, format string, png []byte) {
	if len(png) == 0 {
		return
	}
	key := renderKey(versionID, format)
	if c.rdb != nil {
		_ = c.rdb.Set(ctx, key, png, renderTTL).Err()
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// Keep the in-memory copy bounded: a few hundred renders at ~300KB is
	// as much as a single process should hold.
	if len(c.mem) > 300 {
		oldest, oldestAt := "", time.Now()
		for k, e := range c.mem {
			if e.at.Before(oldestAt) {
				oldest, oldestAt = k, e.at
			}
		}
		delete(c.mem, oldest)
	}
	c.mem[key] = memEntry{png: png, at: time.Now()}
}
