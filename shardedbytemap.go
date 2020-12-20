package cache

import (
	"context"
	"github.com/cespare/xxhash/v2"
	"math/rand"
	"runtime"
	"sync"
	"time"
)

//var _ ReadWriter = &ShardedByteMap{}

//const shards = 64

type hashedBucket struct {
	sync.RWMutex
	data map[uint64]entry
}

type ShardedByteMap struct {
	*shardedByteMap
}

// ShardedByteMap is an in-ShardedByteMap cache.
type shardedByteMap struct {
	hashedBuckets [64]hashedBucket

	*trait
}

// NewShardedByteMap creates an instance of in-ShardedByteMap cache with optional configuration.
func NewShardedByteMap(cfg ...MemoryConfig) *ShardedByteMap {
	c := &shardedByteMap{}
	C := &ShardedByteMap{
		shardedByteMap: c,
	}

	for i := 0; i < shards; i++ {
		c.hashedBuckets[i].data = make(map[uint64]entry)
	}

	c.trait = newTrait(C, cfg...)

	runtime.SetFinalizer(C, func(m *ShardedByteMap) {
		close(c.closed)
	})

	return C
}

// Read gets value.
func (c *ShardedByteMap) Read(ctx context.Context, sharkey []byte) (interface{}, error) {
	if SkipRead(ctx) {
		return nil, ErrCacheItemNotFound
	}

	h := xxhash.Sum64(sharkey)
	b := &c.hashedBuckets[h%shards]
	b.RLock()
	cacheEntry, found := b.data[h]
	b.RUnlock()

	return c.prepareRead(ctx, cacheEntry, found)
}

// Write sets value.
func (c *ShardedByteMap) Write(ctx context.Context, k []byte, v interface{}) error {
	h := xxhash.Sum64(k)
	b := &c.hashedBuckets[h%shards]
	b.Lock()
	defer b.Unlock()

	//ttl := c.config.TimeToLive
	ttl := TTL(ctx)
	if ttl == DefaultTTL {
		ttl = c.config.TimeToLive
	}

	if c.config.ExpirationJitter > 0 {
		ttl += time.Duration(float64(ttl) * c.config.ExpirationJitter * (rand.Float64() - 0.5))
	}

	b.data[h] = entry{Val: v, Exp: time.Now().Add(ttl)}

	if c.log != nil {
		c.log.Debug(ctx, "wrote to cache", "name", c.config.Name, "key", k, "value", v, "ttl", ttl)
	}

	if c.stat != nil {
		c.stat.Add(ctx, MetricWrite, 1, "name", c.config.Name)
	}

	return nil
}

// ExpireAll marks all entries as expired, they can still serve stale cache.
func (c *ShardedByteMap) ExpireAll() {
	//now := time.Now()

	//c.Lock()
	//for k, v := range c.data {
	//	v.Exp = now
	//	c.data[k] = v
	//}
	//c.Unlock()
}

// RemoveAll deletes all entries.
func (c *ShardedByteMap) RemoveAll() {
	//c.Lock()
	//c.data = make(map[string]entry)
	//c.Unlock()
}

func (c *ShardedByteMap) clearExpiredBefore(expirationBoundary time.Time) {
	//keys := make([]string, 0, 100)
	//
	//c.RLock()
	//for k, i := range c.data {
	//	if i.Exp.Before(expirationBoundary) {
	//		keys = append(keys, k)
	//	}
	//}
	//c.RUnlock()
	//
	//if c.log != nil {
	//	c.log.Debug(context.Background(), "clearing expired cache items",
	//		"name", c.config.Name,
	//		"items", keys,
	//	)
	//}
	//
	//c.Lock()
	//for _, k := range keys {
	//	delete(c.data, k)
	//}
	//c.Unlock()

	//c.evictHeapInUse()
}

// Len returns number of elements in cache.
func (c *ShardedByteMap) Len() int {
	//c.RLock()
	//cnt := len(c.data)
	//c.RUnlock()
	//
	//return cnt

	return 0
}

// Walk walks cached entries.
func (c *ShardedByteMap) Walk(walkFn func(key string, value Entry) error) (int, error) {
	//c.RLock()
	//defer c.RUnlock()
	//
	//n := 0
	//
	//for k, v := range c.data {
	//	c.RUnlock()
	//
	//	err := walkFn(k, v)
	//
	//	c.RLock()
	//
	//	if err != nil {
	//		return n, err
	//	}
	//
	//	n++
	//}
	//
	//return n, nil

	return 0, nil
}
