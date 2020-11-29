package cache

import (
	"context"
	"math/rand"
	"runtime"
	"sync"
	"time"

	"github.com/bool64/ctxd"
	"github.com/bool64/stats"
)

// entry is a cache entry.
type entry struct {
	Val interface{}
	Exp time.Time
}

func (e entry) Value() interface{} {
	return e.Val
}

func (e entry) ExpireAt() time.Time {
	return e.Exp
}

type errExpired struct {
	entry entry
}

func (e errExpired) Error() string {
	return ErrExpiredCacheItem.Error()
}

func (e errExpired) Value() interface{} {
	return e.entry.Val
}

func (e errExpired) ExpiredAt() time.Time {
	return e.entry.Exp
}

func (e errExpired) Is(err error) bool {
	return err == ErrExpiredCacheItem
}

// MemoryConfig controls in-memory cache instance.
type MemoryConfig struct {
	// Logger is an instance of contextualized logger, can be nil.
	Logger ctxd.Logger

	// Stats is metrics collector, can be nil.
	Stats stats.Tracker

	// Name is cache instance name, used in stats and logging.
	Name string

	// TimeToLive is delay before entry expiration, default 5m.
	TimeToLive time.Duration

	// DeleteExpiredAfter is delay before expired entry is deleted from cache, default 24h.
	DeleteExpiredAfter time.Duration

	// DeleteExpiredJobInterval is delay between two consecutive cleanups, default 1h.
	DeleteExpiredJobInterval time.Duration

	// ItemsCountReportInterval is items count metric report interval, default 1m.
	ItemsCountReportInterval time.Duration

	// ExpirationJitter is a fraction of TTL to randomize, default 0.1.
	// Use -1 to disable.
	// If enabled, entry TTL will be randomly altered in bounds of ±(ExpirationJitter * TTL / 2).
	ExpirationJitter float64

	// HeapInUseSoftLimit sets heap in use threshold when eviction of most expired items will be performed.
	//
	// Eviction is a part of delete expired job, eviction runs at most once per delete expired job and
	// removes most expired entries up to HeapInUseEvictFraction.
	HeapInUseSoftLimit uint64

	// HeapInUseEvictFraction is a fraction of total count of items to be evicted (0, 1], default 0.1 (10% of items).
	HeapInUseEvictFraction float64
}

var _ ReadWriter = &memory{}

type Memory struct {
	*memory
}

// memory is an in-memory cache.
type memory struct {
	sync.RWMutex
	data   map[string]entry
	closed chan struct{}

	config MemoryConfig
	log    ctxd.Logger
	stat   stats.Tracker
}

// NewMemory creates an instance of in-memory cache with optional configuration.
func NewMemory(cfg ...MemoryConfig) *Memory {
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

	c := &memory{
		data:   map[string]entry{},
		config: config,
		stat:   config.Stats,
		log:    config.Logger,
		closed: make(chan struct{}),
	}

	if c.stat != nil {
		go c.reportItemsCount()
	}

	go c.cleaner()

	m := &Memory{
		memory: c,
	}

	runtime.SetFinalizer(m, func(m *Memory) {
		close(c.closed)
	})

	return m
}

func (c *memory) prepareRead(ctx context.Context, cacheEntry entry, found bool) (interface{}, error) {
	if !found {
		if c.log != nil {
			c.log.Debug(ctx, "cache miss", "name", c.config.Name)
		}

		if c.stat != nil {
			c.stat.Add(ctx, MetricMiss, 1, "name", c.config.Name)
		}

		return nil, ErrCacheItemNotFound
	}

	if cacheEntry.Exp.Before(time.Now()) {
		if c.log != nil {
			c.config.Logger.Debug(ctx, "cache key expired", "name", c.config.Name)
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
			"entry", cacheEntry,
		)
	}

	return cacheEntry.Val, nil
}

// Read gets value.
func (c *memory) Read(ctx context.Context, k string) (interface{}, error) {
	if SkipRead(ctx) {
		return nil, ErrCacheItemNotFound
	}

	c.RLock()
	cacheEntry, found := c.data[k]
	c.RUnlock()

	return c.prepareRead(ctx, cacheEntry, found)
}

// Write sets value.
func (c *memory) Write(ctx context.Context, k string, v interface{}) error {
	c.Lock()
	defer c.Unlock()

	//ttl := c.config.TimeToLive
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
func (c *memory) ExpireAll() {
	now := time.Now()

	c.Lock()
	for k, v := range c.data {
		v.Exp = now
		c.data[k] = v
	}
	c.Unlock()
}

// RemoveAll deletes all entries.
func (c *memory) RemoveAll() {
	c.Lock()
	c.data = make(map[string]entry)
	c.Unlock()
}

func (c *memory) cleaner() {
	for {
		interval := c.config.DeleteExpiredJobInterval

		select {
		case <-time.After(interval):
			c.clearExpired()
		case <-c.closed:
			if c.log != nil {
				c.log.Debug(context.Background(), "exiting expired cache cleaner",
					"name", c.config.Name)
			}
			return
		}
	}
}

func (c *memory) clearExpired() {
	expirationBoundary := time.Now().Add(-c.config.DeleteExpiredAfter)
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

func (c *memory) reportItemsCount() {
	for {
		interval := c.config.ItemsCountReportInterval

		select {
		case <-time.After(interval):
			count := c.Len()

			if c.log != nil {
				c.log.Debug(context.Background(), "cache items count",
					"name", c.config.Name,
					"count", c.Len(),
				)
			}

			if c.stat != nil {
				c.stat.Set(context.Background(), MetricItems, float64(count), "name", c.config.Name)
			}
		case <-c.closed:
			if c.log != nil {
				c.log.Debug(context.Background(), "exiting cache items counter goroutine",
					"name", c.config.Name)
			}
			if c.stat != nil {
				c.stat.Set(context.Background(), MetricItems, float64(c.Len()), "name", c.config.Name)
			}
			return
		}
	}
}

// Len returns number of elements in cache.
func (c *memory) Len() int {
	c.RLock()
	cnt := len(c.data)
	c.RUnlock()

	return cnt
}

// Walk walks cached entries.
func (c *memory) Walk(walkFn func(key string, value Entry) error) (int, error) {
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
