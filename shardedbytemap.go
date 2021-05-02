package cache

import (
	"bytes"
	"context"
	"math/rand"
	"runtime"
	"sync"
	"time"

	"github.com/cespare/xxhash/v2"
)

var (
	_ ReadWriter = &shardedMap{}
	_ Deleter    = &shardedMap{}
)

const shards = 128

type hashedBucket struct {
	sync.RWMutex
	data map[uint64]entry
}

// ShardedMap is an in-memory cache backend.
type ShardedMap struct {
	*shardedMap
}

type shardedMap struct {
	hashedBuckets [shards]hashedBucket

	*trait
}

// NewShardedMap creates an instance of in-memory cache with optional configuration.
func NewShardedMap(cfg ...MemoryConfig) *ShardedMap {
	c := &shardedMap{}
	C := &ShardedMap{
		shardedMap: c,
	}

	for i := 0; i < shards; i++ {
		c.hashedBuckets[i].data = make(map[uint64]entry)
	}

	c.trait = newTrait(C, cfg...)

	runtime.SetFinalizer(C, func(m *ShardedMap) {
		close(c.closed)
	})

	return C
}

// Read gets value.
func (c *shardedMap) Read(ctx context.Context, key []byte) (interface{}, error) {
	if SkipRead(ctx) {
		return nil, ErrCacheItemNotFound
	}

	h := xxhash.Sum64(key)
	b := &c.hashedBuckets[h%shards]
	b.RLock()
	cacheEntry, found := b.data[h]
	b.RUnlock()

	return c.prepareRead(ctx, cacheEntry, found)
}

// Write sets value.
func (c *shardedMap) Write(ctx context.Context, k []byte, v interface{}) error {
	h := xxhash.Sum64(k)
	b := &c.hashedBuckets[h%shards]
	b.Lock()
	defer b.Unlock()

	// ttl := c.config.TimeToLive
	ttl := TTL(ctx)
	if ttl == DefaultTTL {
		ttl = c.config.TimeToLive
	}

	if c.config.ExpirationJitter > 0 {
		ttl += time.Duration(float64(ttl) * c.config.ExpirationJitter * (rand.Float64() - 0.5)) // nolint:gosec
	}

	// Copy key to allow mutations of original argument.
	key := make([]byte, len(k))
	copy(key, k)

	b.data[h] = entry{V: v, K: key, E: time.Now().Add(ttl)}

	if c.log != nil {
		c.log.Debug(ctx, "wrote to cache", "name", c.config.Name, "key", key, "value", v, "ttl", ttl)
	}

	if c.stat != nil {
		c.stat.Add(ctx, MetricWrite, 1, "name", c.config.Name)
	}

	return nil
}

func (c *shardedMap) Delete(ctx context.Context, key []byte) error {
	h := xxhash.Sum64(key)
	b := &c.hashedBuckets[h%shards]

	b.Lock()
	defer b.Unlock()

	cachedEntry, found := b.data[h]
	if !found || !bytes.Equal(cachedEntry.K, key) {
		return ErrCacheItemNotFound
	}

	delete(b.data, h)

	return nil
}

// ExpireAll marks all entries as expired, they can still serve stale cache.
func (c *shardedMap) ExpireAll() {
	now := time.Now()

	for i := range c.hashedBuckets {
		b := &c.hashedBuckets[i]
		b.Lock()
		for h, v := range b.data {
			v.E = now
			b.data[h] = v
		}
		b.Unlock()
	}
}

// RemoveAll deletes all entries.
func (c *shardedMap) RemoveAll() {
	for i := range c.hashedBuckets {
		b := &c.hashedBuckets[i]

		b.Lock()
		for h := range c.hashedBuckets[i].data {
			delete(b.data, h)
		}
		b.Unlock()
	}
}

func (c *shardedMap) clearExpiredBefore(expirationBoundary time.Time) {
	for i := range c.hashedBuckets {
		b := &c.hashedBuckets[i]

		b.Lock()
		for h, v := range b.data {
			if v.E.Before(expirationBoundary) {
				delete(b.data, h)
			}
		}
		b.Unlock()
	}

	c.evictHeapInUse()
}

// Len returns number of elements in cache.
func (c *shardedMap) Len() int {
	cnt := 0

	for i := range c.hashedBuckets {
		b := &c.hashedBuckets[i]

		b.RLock()
		cnt += len(b.data)
		b.RUnlock()
	}

	return cnt
}

// Walk walks cached entries.
func (c *shardedMap) Walk(walkFn func(e Entry) error) (int, error) {
	n := 0

	for i := range c.hashedBuckets {
		b := &c.hashedBuckets[i]
		b.RLock()
		for _, v := range c.hashedBuckets[i].data {
			b.RUnlock()

			err := walkFn(v)
			if err != nil {
				return n, err
			}

			n++

			b.RLock()
		}
		b.RUnlock()
	}

	return n, nil
}
