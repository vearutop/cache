package cache

import (
	"context"
	"math/rand"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/bool64/ctxd"
	"github.com/bool64/stats"
)

var _ ReadWriter = &SyncMap{}

// Memory is an in-memory cache.
type SyncMap struct {
	data   sync.Map
	closed chan struct{}

	config MemoryConfig
	log    ctxd.Logger
	stat   stats.Tracker
}

// NewSyncMap creates an instance of in-memory cache with optional configuration.
func NewSyncMap(cfg ...MemoryConfig) *SyncMap {
	config := MemoryConfig{}

	if len(cfg) >= 1 {
		config = cfg[0]
	}

	if config.DeleteExpiredAfter == 0 {
		config.DeleteExpiredAfter = 24 * time.Hour
	}

	if config.DeleteExpiredJobInterval == 0 {
		config.DeleteExpiredJobInterval = time.Hour
	}

	if config.ItemsCountReportInterval == 0 {
		config.ItemsCountReportInterval = time.Minute
	}

	if config.ExpirationJitter == 0 {
		config.ExpirationJitter = 0.1
	}

	if config.TimeToLive == 0 {
		config.TimeToLive = 5 * time.Minute
	}

	c := &SyncMap{
		config: config,
		stat:   config.Stats,
		log:    config.Logger,
		closed: make(chan struct{}, 1),
	}

	if c.stat != nil {
		go c.reportItemsCount()
	}

	go c.cleaner()

	return c
}

// Read gets value.
func (c *SyncMap) Read(ctx context.Context, k string) (interface{}, error) {
	if SkipRead(ctx) {
		return nil, ErrCacheItemNotFound
	}

	v, ok := c.data.Load(k)
	cacheEntry := v.(entry)
	if !ok {
		if c.log != nil {
			c.log.Debug(ctx, "cache miss",
				"name", c.config.Name,
				"key", k)
		}

		if c.stat != nil {
			c.stat.Add(ctx, MetricMiss, 1, "name", c.config.Name)
		}

		return nil, ErrCacheItemNotFound
	}

	if cacheEntry.Exp.Before(time.Now()) {
		if c.log != nil {
			c.config.Logger.Debug(ctx, "cache key expired",
				"name", c.config.Name,
				"key", k)
		}

		if c.stat != nil {
			c.stat.Add(ctx, MetricExpired, 1, "name", c.config.Name)
		}

		return cacheEntry.Val, errExpired{entry: cacheEntry}
	}

	if c.stat != nil {
		c.stat.Add(ctx, MetricHit, 1, "name", c.config.Name)
	}

	if c.log != nil {
		c.log.Debug(ctx, "cache hit",
			"name", c.config.Name,
			"key", k,
			"entry", cacheEntry)
	}

	return cacheEntry.Val, nil
}

// Write sets value.
func (c *SyncMap) Write(ctx context.Context, k string, v interface{}) error {
	ttl := c.config.TimeToLive
	//ttl := TTL(ctx)
	if ttl == DefaultTTL {
		ttl = c.config.TimeToLive
	}

	if c.config.ExpirationJitter > 0 {
		ttl += time.Duration(float64(ttl) * c.config.ExpirationJitter * (rand.Float64() - 0.5))
	}

	c.data.Store(k, entry{Val: v, Exp: time.Now().Add(ttl)})

	if c.log != nil {
		c.log.Debug(ctx, "wrote to cache", "name", c.config.Name, "key", k, "value", v, "ttl", ttl)
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

// Close disables cache instance.
func (c *SyncMap) Close() {
	c.closed <- struct{}{}
}

func (c *SyncMap) cleaner() {
	for {
		interval := c.config.DeleteExpiredJobInterval

		select {
		case <-time.After(interval):
			c.clearExpired()
		case <-c.closed:
			c.RemoveAll()

			return
		}
	}
}

func (c *SyncMap) clearExpired() {
	expirationBoundary := time.Now().Add(-c.config.DeleteExpiredAfter)

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

func (c *SyncMap) reportItemsCount() {
	for {
		interval := c.config.ItemsCountReportInterval
		select {
		case <-c.closed:
			return
		case <-time.After(interval):
			count := c.Len()

			if c.log != nil {
				c.log.Debug(context.Background(), "cache items count",
					"name", c.config.Name,
					"count", count,
				)
			}

			if c.stat != nil {
				c.stat.Set(context.Background(), MetricItems, float64(count), "name", c.config.Name)
			}
		}
	}
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
