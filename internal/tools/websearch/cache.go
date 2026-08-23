package websearch

import (
	"sync"
	"time"
)

// cacheTTL is how long a cached result stays valid.
const cacheTTL = 5 * time.Minute

// cacheKey identifies a search: same provider config, query, and count
// always produce the same result.
type cacheKey struct {
	provider string
	query    string
	count    int
}

type cacheEntry struct {
	result string
	at     time.Time
}

// resultCache is a mutex-guarded map on Tool, shared by every search on
// that instance — including concurrent sub-agents dispatching through the
// same Tool. Its zero value is ready to use.
type resultCache struct {
	mu      sync.Mutex
	entries map[cacheKey]cacheEntry
}

// get returns the cached result for key if present and younger than
// cacheTTL as of now.
func (c *resultCache) get(key cacheKey, now time.Time) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || now.Sub(e.at) >= cacheTTL {
		return "", false
	}
	return e.result, true
}

// put stores result for key, timestamped at now.
func (c *resultCache) put(key cacheKey, result string, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[cacheKey]cacheEntry)
	}
	c.entries[key] = cacheEntry{result: result, at: now}
}
