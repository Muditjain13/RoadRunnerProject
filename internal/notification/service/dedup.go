package service

import (
	"sync"
	"time"
)

// dedupCache is a bounded-by-TTL "have I seen this key recently" set. Not
// a general-purpose cache: checkAndMark both checks and records in one
// call, which is exactly what a consumer's dedup check needs and nothing
// more.
type dedupCache struct {
	mu   sync.Mutex
	seen map[string]time.Time
	ttl  time.Duration
	now  func() time.Time
}

func newDedupCache(ttl time.Duration) *dedupCache {
	return &dedupCache{seen: make(map[string]time.Time), ttl: ttl, now: time.Now}
}

// checkAndMark returns true if key was already marked within ttl (a
// duplicate), and records key as seen now if it wasn't. Also opportunistically
// evicts expired entries so the map doesn't grow without bound.
func (c *dedupCache) checkAndMark(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	if seenAt, ok := c.seen[key]; ok && now.Sub(seenAt) < c.ttl {
		return true
	}

	c.seen[key] = now
	for k, seenAt := range c.seen {
		if now.Sub(seenAt) >= c.ttl {
			delete(c.seen, k)
		}
	}
	return false
}
