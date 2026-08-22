package websearch

import (
	"testing"
	"time"
)

func TestResultCache_GetMissOnEmpty(t *testing.T) {
	var c resultCache
	if _, ok := c.get(cacheKey{query: "x"}, time.Now()); ok {
		t.Error("get on empty cache: want ok=false")
	}
}

func TestResultCache_PutThenGet(t *testing.T) {
	var c resultCache
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	key := cacheKey{provider: "auto", query: "q", count: 5}
	c.put(key, "result", now)

	got, ok := c.get(key, now.Add(time.Minute))
	if !ok || got != "result" {
		t.Errorf("get() = (%q, %v), want (%q, true)", got, ok, "result")
	}
}

func TestResultCache_ExpiresAtTTL(t *testing.T) {
	var c resultCache
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	key := cacheKey{query: "q"}
	c.put(key, "result", now)

	if _, ok := c.get(key, now.Add(cacheTTL-time.Nanosecond)); !ok {
		t.Error("get() just before TTL: want ok=true")
	}
	if _, ok := c.get(key, now.Add(cacheTTL)); ok {
		t.Error("get() at TTL boundary: want ok=false (entry is stale)")
	}
}

func TestResultCache_DistinctKeys(t *testing.T) {
	var c resultCache
	now := time.Now()
	c.put(cacheKey{provider: "auto", query: "a", count: 5}, "A", now)
	c.put(cacheKey{provider: "auto", query: "b", count: 5}, "B", now)

	if got, ok := c.get(cacheKey{provider: "auto", query: "a", count: 5}, now); !ok || got != "A" {
		t.Errorf("get(a) = (%q, %v), want (%q, true)", got, ok, "A")
	}
	if _, ok := c.get(cacheKey{provider: "auto", query: "a", count: 6}, now); ok {
		t.Error("get() with a different count: want ok=false")
	}
}
