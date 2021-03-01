package cache

import (
	"context"
	"math/rand"
	"runtime"
	"sync"
	"time"
)

var _ ReadWriter = &MutexMap{}

type MutexMap struct {
	*mutexMap
}

// RWMutexMap is an in-rwMutexMap cache.
type mutexMap struct {
	sync.Mutex
	data map[string]entry

	*trait
}

// NewRWMutexMap creates an instance of in-rwMutexMap cache with optional configuration.
func NewMutexMap(cfg ...MemoryConfig) *MutexMap {
	c := &mutexMap{
		data: map[string]entry{},
	}
	C := &MutexMap{
		mutexMap: c,
	}

	c.trait = newTrait(C, cfg...)

	runtime.SetFinalizer(C, func(m *MutexMap) {
		close(c.closed)
	})

	return C
}

// Read gets value.
func (c *MutexMap) Read(ctx context.Context, mtxkey string) (interface{}, error) {
	if SkipRead(ctx) {
		return nil, ErrCacheItemNotFound
	}

	c.Lock()
	cacheEntry, found := c.data[mtxkey]
	c.Unlock()

	return c.prepareRead(ctx, cacheEntry, found)
}

// Write sets value.
func (c *MutexMap) Write(ctx context.Context, k string, v interface{}) error {
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
func (c *MutexMap) ExpireAll() {
	now := time.Now()

	c.Lock()
	for k, v := range c.data {
		v.Exp = now
		c.data[k] = v
	}
	c.Unlock()
}

// RemoveAll deletes all entries.
func (c *MutexMap) RemoveAll() {
	c.Lock()
	c.data = make(map[string]entry)
	c.Unlock()
}

func (c *MutexMap) clearExpiredBefore(expirationBoundary time.Time) {
	keys := make([]string, 0, 100)

	c.Lock()
	for k, i := range c.data {
		if i.Exp.Before(expirationBoundary) {
			keys = append(keys, k)
		}
	}
	c.Unlock()

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
}

// Len returns number of elements in cache.
func (c *MutexMap) Len() int {
	c.Lock()
	cnt := len(c.data)
	c.Unlock()

	return cnt
}

// Walk walks cached entries.
func (c *MutexMap) Walk(walkFn func(key string, value Entry) error) (int, error) {
	c.Lock()
	defer c.Unlock()

	n := 0

	for k, v := range c.data {
		c.Unlock()

		err := walkFn(k, v)

		c.Lock()

		if err != nil {
			return n, err
		}

		n++
	}

	return n, nil
}
