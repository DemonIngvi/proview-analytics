// Package cache is a small TTL cache with request coalescing. Dashboards
// poll the same slices from many browsers; coalescing turns N identical
// concurrent requests into one ClickHouse query.
package cache

import (
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

type entry struct {
	v       any
	expires time.Time
}

// Cache maps keys to values for a fixed TTL.
type Cache struct {
	mu    sync.Mutex
	m     map[string]entry
	ttl   time.Duration
	max   int
	group singleflight.Group
	now   func() time.Time
}

// New creates a cache holding at most max entries.
func New(ttl time.Duration, max int) *Cache {
	return &Cache{m: make(map[string]entry), ttl: ttl, max: max, now: time.Now}
}

// Do returns the cached value for key or computes it with fn. Errors are not cached.
func (c *Cache) Do(key string, fn func() (any, error)) (any, error) {
	if c.ttl <= 0 {
		return fn()
	}
	c.mu.Lock()
	if e, ok := c.m[key]; ok && c.now().Before(e.expires) {
		c.mu.Unlock()
		return e.v, nil
	}
	c.mu.Unlock()

	v, err, _ := c.group.Do(key, func() (any, error) {
		v, err := fn()
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if len(c.m) >= c.max {
			c.evict()
		}
		c.m[key] = entry{v: v, expires: c.now().Add(c.ttl)}
		return v, nil
	})
	return v, err
}

// evict drops expired entries, or everything when none expired. Called with mu held.
func (c *Cache) evict() {
	now := c.now()
	for k, e := range c.m {
		if !now.Before(e.expires) {
			delete(c.m, k)
		}
	}
	if len(c.m) >= c.max {
		clear(c.m)
	}
}

// Len returns the number of entries.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}
