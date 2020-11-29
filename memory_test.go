package cache_test

import (
	"context"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/bool64/cache"
	"github.com/bool64/ctxd"
	"github.com/bool64/stats"
	"github.com/stretchr/testify/assert"
)

func TestMemory(t *testing.T) {
	ctx := context.Background()
	logger := ctxd.NoOpLogger{}
	st := stats.TrackerMock{}
	cfg := cache.MemoryConfig{
		Name:                     "test",
		Stats:                    &st,
		Logger:                   logger,
		TimeToLive:               time.Millisecond,
		DeleteExpiredAfter:       20 * time.Millisecond,
		DeleteExpiredJobInterval: 8 * time.Millisecond,
		ItemsCountReportInterval: 10 * time.Millisecond,
	}
	mc := cache.NewMemory(cfg)
	val, err := mc.Read(ctx, "key")
	assert.Nil(t, val)
	assert.EqualError(t, err, cache.ErrCacheItemNotFound.Error())

	err = mc.Write(ctx, "key", 123)
	assert.NoError(t, err)

	val, err = mc.Read(ctx, "key")
	assert.Equal(t, 123, val)
	assert.NoError(t, err)

	// Expired.
	time.Sleep(time.Millisecond)

	val, err = mc.Read(ctx, "key")
	assert.Equal(t, 123, val)
	assert.EqualError(t, err, cache.ErrExpiredCacheItem.Error())

	// Deleted.
	time.Sleep(100 * time.Millisecond)
	runtime.Gosched()

	val, err = mc.Read(ctx, "key")
	assert.Nil(t, val)
	assert.EqualError(t, err, cache.ErrCacheItemNotFound.Error())

	err = mc.Write(cache.WithTTL(ctx, 100*time.Millisecond), "key", 123)
	assert.NoError(t, err)
	mc.ExpireAll()

	// Forced expiration.
	time.Sleep(time.Millisecond)

	val, err = mc.Read(ctx, "key")
	assert.Equal(t, 123, val)
	assert.EqualError(t, err, cache.ErrExpiredCacheItem.Error())

	mc.Close()
	time.Sleep(time.Millisecond)

	assert.Equal(
		t,
		map[string]float64{"cache_expired": 2, "cache_hit": 1, "cache_items": 0, "cache_miss": 2, "cache_write": 2},
		st.Values(),
	)

	// Closed.
	val, err = mc.Read(ctx, "key")
	assert.Nil(t, val)
	assert.EqualError(t, err, "cache is closed")

	err = mc.Write(ctx, "key", 123)
	assert.EqualError(t, err, "cache is closed")
}

func TestMemory_Read_concurrency(t *testing.T) {
	st := &stats.TrackerMock{}
	c := cache.NewMemory(cache.MemoryConfig{
		Stats: st,
	})
	ctx := context.Background()

	pipeline := make(chan struct{}, 500)
	n := 1000

	for i := 0; i < n; i++ {
		pipeline <- struct{}{}

		k := "oneone" + strconv.Itoa(i)

		go func() {
			defer func() {
				<-pipeline
			}()

			err := c.Write(ctx, k, 123)
			assert.NoError(t, err)

			v, err := c.Read(ctx, k)
			assert.NoError(t, err)
			assert.Equal(t, 123, v)
		}()
	}

	// Waiting for goroutines to finish.
	for i := 0; i < cap(pipeline); i++ {
		pipeline <- struct{}{}
	}

	// Every distinct key has single build and write.
	assert.Equal(t, n, st.Int(cache.MetricWrite), "total writes")

	// Written value is returned without hitting cache.
	assert.Equal(t, n, st.Int(cache.MetricHit))
}
