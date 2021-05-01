package cache

import (
	"context"
	"math/rand"
	"runtime"
	"sync"
	"time"
)

var _ ReadWriter = &RWMutexMap{}

// RWMutexMap is an in-memory cache.
type RWMutexMap struct {
	*rwMutexMap
}

type rwMutexMap struct {
	sync.RWMutex
	data map[string]entry

	*trait
}

// NewRWMutexMap creates an instance of in-rwMutexMap cache with optional configuration.
func NewRWMutexMap(cfg ...MemoryConfig) *RWMutexMap {
	c := &rwMutexMap{
		data: map[string]entry{},
	}
	C := &RWMutexMap{
		rwMutexMap: c,
	}

	c.trait = newTrait(C, cfg...)

	runtime.SetFinalizer(C, func(m *RWMutexMap) {
		close(c.closed)
	})

	return C
}

// Read gets value.
func (c *RWMutexMap) Read(ctx context.Context, k string) (interface{}, error) {
	if SkipRead(ctx) {
		return nil, ErrCacheItemNotFound
	}

	c.RLock()
	cacheEntry, found := c.data[k]
	c.RUnlock()

	return c.prepareRead(ctx, cacheEntry, found)
}

// Write sets value.
func (c *RWMutexMap) Write(ctx context.Context, k string, v interface{}) error {
	c.Lock()
	defer c.Unlock()

	// ttl := c.config.TimeToLive
	ttl := TTL(ctx)
	if ttl == DefaultTTL {
		ttl = c.config.TimeToLive
	}

	if c.config.ExpirationJitter > 0 {
		ttl += time.Duration(float64(ttl) * c.config.ExpirationJitter * (rand.Float64() - 0.5))
	}

	c.data[k] = entry{Val: v, Exp: time.Now().Add(ttl)}

	if c.log != nil {
		c.log.Debug(ctx, "wrote to cache", "name", c.config.Name, "key", k, "value", v, "ttl", ttl)
	}

	if c.stat != nil {
		c.stat.Add(ctx, MetricWrite, 1, "name", c.config.Name)
	}

	return nil
}

// ExpireAll marks all entries as expired, they can still serve stale cache.
func (c *RWMutexMap) ExpireAll() {
	now := time.Now()

	c.Lock()
	for k, v := range c.data {
		v.Exp = now
		c.data[k] = v
	}
	c.Unlock()
}

// RemoveAll deletes all entries.
func (c *RWMutexMap) RemoveAll() {
	c.Lock()
	c.data = make(map[string]entry)
	c.Unlock()
}

func (c *RWMutexMap) clearExpiredBefore(expirationBoundary time.Time) {
	keys := make([]string, 0, 100)

	c.RLock()
	for k, i := range c.data {
		if i.Exp.Before(expirationBoundary) {
			keys = append(keys, k)
		}
	}
	c.RUnlock()

	if c.log != nil {
		c.log.Debug(context.Background(), "clearing expired cache items",
			"name", c.config.Name,
			"items", keys,
		)
	}

	c.Lock()
	for _, k := range keys {
		delete(c.data, k)
	}
	c.Unlock()

	c.evictHeapInUse()
}

// Len returns number of elements in cache.
func (c *RWMutexMap) Len() int {
	c.RLock()
	cnt := len(c.data)
	c.RUnlock()

	return cnt
}

// Walk walks cached entries.
func (c *RWMutexMap) Walk(walkFn func(key string, value Entry) error) (int, error) {
	c.RLock()
	defer c.RUnlock()

	n := 0

	for k, v := range c.data {
		c.RUnlock()

		err := walkFn(k, v)

		c.RLock()

		if err != nil {
			return n, err
		}

		n++
	}

	return n, nil
}
