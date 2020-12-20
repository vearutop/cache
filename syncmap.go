package cache

import (
	"context"
	"math/rand"
	"runtime"
	"sort"
	"sync"
	"time"
)

var _ ReadWriter = &SyncMap{}

type SyncMap struct {
	*syncMap
}

// SyncMap is an in-rwMutexMap cache.
type syncMap struct {
	data sync.Map

	*trait
}

// NewSyncMap creates an instance of in-rwMutexMap cache with optional configuration.
func NewSyncMap(cfg ...MemoryConfig) *SyncMap {
	c := &syncMap{}

	C := &SyncMap{
		syncMap: c,
	}

	c.trait = newTrait(C, cfg...)

	runtime.SetFinalizer(C, func(m *SyncMap) {
		close(c.closed)
	})

	return C
}

// Read gets value.
func (c *SyncMap) Read(ctx context.Context, key string) (interface{}, error) {
	if SkipRead(ctx) {
		return nil, ErrCacheItemNotFound
	}

	v, found := c.data.Load(key)
	var cacheEntry entry
	if v != nil {
		cacheEntry = v.(entry)
	}

	return c.prepareRead(ctx, cacheEntry, found)
}

// Write sets value.
func (c *SyncMap) Write(ctx context.Context, key string, value interface{}) error {
	ttl := TTL(ctx)
	if ttl == DefaultTTL {
		ttl = c.config.TimeToLive
	}

	if c.config.ExpirationJitter > 0 {
		ttl += time.Duration(float64(ttl) * c.config.ExpirationJitter * (rand.Float64() - 0.5))
	}

	c.data.Store(key, entry{Val: value, Exp: time.Now().Add(ttl)})

	if c.log != nil {
		c.log.Debug(ctx, "wrote to cache", "name", c.config.Name, "key", key, "value", value, "ttl", ttl)
	}

	if c.stat != nil {
		c.stat.Add(ctx, MetricWrite, 1, "name", c.config.Name)
	}

	return nil
}

// ExpireAll marks all entries as expired, they can still serve stale cache.
func (c *SyncMap) ExpireAll() {
	now := time.Now()

	c.data.Range(func(key, value interface{}) bool {
		v := value.(entry) // TODO consider pointer semantics to optimize allocations.
		v.Exp = now
		c.data.Store(key, v)

		return true
	})
}

// RemoveAll deletes all entries.
func (c *SyncMap) RemoveAll() {
	c.data.Range(func(key, value interface{}) bool {
		c.data.Delete(key)

		return true
	})
}

func (c *SyncMap) clearExpiredBefore(expirationBoundary time.Time) {
	c.data.Range(func(key, value interface{}) bool {
		v := value.(entry)

		if v.Exp.Before(expirationBoundary) {
			c.data.Delete(key)
		}

		if c.log != nil {
			c.log.Debug(context.Background(), "clearing expired cache item",
				"name", c.config.Name,
				"key", key,
			)
		}

		return true
	})

	c.evictHeapInUse()
}

// Len returns number of elements in cache.
func (c *SyncMap) Len() int {
	count := 0
	c.data.Range(func(key, value interface{}) bool {
		count++

		return true
	})

	return count
}

// Walk walks cached entries.
func (c *SyncMap) Walk(walkFn func(key string, value Entry) error) (int, error) {
	n := 0
	var resultErr error

	c.data.Range(func(key, value interface{}) bool {
		err := walkFn(key.(string), value.(entry))
		if err != nil {
			resultErr = err

			return false
		}

		n++

		return true
	})

	return n, resultErr
}

func (c *SyncMap) evictHeapInUse() {
	if c.config.HeapInUseSoftLimit == 0 {
		return
	}

	runtime.GC()

	m := runtime.MemStats{}
	runtime.ReadMemStats(&m)

	if m.HeapInuse < c.config.HeapInUseSoftLimit {
		return
	}

	type item struct {
		key      string
		expireAt time.Time
	}

	entries := make([]item, 0, 100)

	// Collect all keys and expirations.
	c.data.Range(func(key, value interface{}) bool {
		entries = append(entries, item{key: key.(string), expireAt: value.(entry).Exp})

		return true
	})

	// Sort entries to put most expired in head.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].expireAt.Before(entries[j].expireAt)
	})

	evictFraction := c.config.HeapInUseEvictFraction
	if evictFraction == 0 {
		evictFraction = 0.1
	}

	evictItems := int(float64(len(entries)) * evictFraction)

	if c.stat != nil {
		c.stat.Add(context.Background(), MetricEvict, float64(evictItems), "name", c.config.Name)
	}

	for i := 0; i < evictItems; i++ {
		c.data.Delete(entries[i].key)
	}
}
