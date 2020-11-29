package cache

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"time"

	"github.com/bool64/ctxd"
	"github.com/bool64/stats"
)

// errMemoryCacheIsClosed indicates cache was closed and deactivated.
var errMemoryCacheIsClosed = errors.New("cache is closed")

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

var _ ReadWriter = &Memory{}

// Memory is an in-memory cache.
type Memory struct {
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

	c := &Memory{
		data:   map[string]entry{},
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
func (c *Memory) Read(ctx context.Context, k string) (interface{}, error) {
	if SkipRead(ctx) {
		return nil, ErrCacheItemNotFound
	}

	closed := false
	c.RLock()
	if c.data == nil {
		closed = true
	}

	cacheEntry, ok := c.data[k]
	c.RUnlock()

	if closed {
		return nil, errMemoryCacheIsClosed // TODO: remove closer with GC.
	}

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
func (c *Memory) Write(ctx context.Context, k string, v interface{}) error {
	c.Lock()
	defer c.Unlock()

	if c.data == nil {
		if c.log != nil {
			c.log.Debug(ctx, "writing to a closed cache", "name", c.config.Name, "key", k)
		}

		return errMemoryCacheIsClosed
	}

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
func (c *Memory) ExpireAll() {
	now := time.Now()

	c.Lock()
	for k, v := range c.data {
		v.Exp = now
		c.data[k] = v
	}
	c.Unlock()
}

// RemoveAll deletes all entries.
func (c *Memory) RemoveAll() {
	c.Lock()
	c.data = make(map[string]entry)
	c.Unlock()
}

// Close disables cache instance.
func (c *Memory) Close() {
	c.closed <- struct{}{}
}

func (c *Memory) cleaner() {
	for {
		c.RLock()
		interval := c.config.DeleteExpiredJobInterval
		c.RUnlock()

		select {
		case <-time.After(interval):
			c.clearExpired()
		case <-c.closed:
			c.Lock()
			c.data = nil
			c.Unlock()

			return
		}
	}
}

func (c *Memory) clearExpired() {
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

func (c *Memory) reportItemsCount() {
	for {
		c.RLock()
		interval := c.config.ItemsCountReportInterval
		c.RUnlock()

		<-time.After(interval)
		c.RLock()
		closed := c.data == nil
		count := len(c.data)
		c.RUnlock()

		if closed {
			return
		}

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

// Len returns number of elements in cache.
func (c *Memory) Len() int {
	c.RLock()
	cnt := len(c.data)
	c.RUnlock()

	return cnt
}

// Walk walks cached entries.
func (c *Memory) Walk(walkFn func(key string, value Entry) error) (int, error) {
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
