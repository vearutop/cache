package cache

import (
	"context"
	"github.com/bool64/ctxd"
	"github.com/bool64/stats"
	"time"
)

type trait struct {
	closed chan struct{}

	config MemoryConfig
	log    ctxd.Logger
	stat   stats.Tracker
}

type backend interface {
	Len() int
	clearExpiredBefore(t time.Time)
}

func newTrait(b backend, cfg ...MemoryConfig) *trait {
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

	t := &trait{
		config: config,
		stat:   config.Stats,
		log:    config.Logger,
		closed: make(chan struct{}),
	}

	if t.stat != nil {
		go t.reportItemsCount(b)
	}

	go t.cleaner(b)

	return t
}

func (c *trait) prepareRead(ctx context.Context, cacheEntry entry, found bool) (interface{}, error) {
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

func (c *trait) reportItemsCount(b backend) {
	for {
		interval := c.config.ItemsCountReportInterval

		select {
		case <-time.After(interval):
			count := b.Len()

			if c.log != nil {
				c.log.Debug(context.Background(), "cache items count",
					"name", c.config.Name,
					"count", b.Len(),
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
				c.stat.Set(context.Background(), MetricItems, float64(b.Len()), "name", c.config.Name)
			}
			return
		}
	}
}

func (c *trait) cleaner(b backend) {
	for {
		interval := c.config.DeleteExpiredJobInterval

		select {
		case <-time.After(interval):
			expirationBoundary := time.Now().Add(-c.config.DeleteExpiredAfter)
			b.clearExpiredBefore(expirationBoundary)
		case <-c.closed:
			if c.log != nil {
				c.log.Debug(context.Background(), "exiting expired cache cleaner",
					"name", c.config.Name)
			}
			return
		}
	}
}

// MemoryConfig controls in-RWMutexMap cache instance.
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
