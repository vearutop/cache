package cache_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bool64/cache"
	"github.com/stretchr/testify/assert"
)

func TestInvalidator_Invalidate(t *testing.T) {
	cache1 := cache.NewShardedMap()
	cache2 := cache.NewShardedMap()

	i := &cache.Invalidator{}
	err := i.Invalidate()
	assert.Error(t, err) // nothing to invalidate

	ctx := context.Background()

	i.Callbacks = append(i.Callbacks, cache1.ExpireAll, cache2.ExpireAll)

	assert.NoError(t, cache1.Write(ctx, []byte("key"), 1))
	assert.NoError(t, cache2.Write(ctx, []byte("key"), 2))

	val, err := cache1.Read(ctx, []byte("key"))
	assert.NoError(t, err)
	assert.Equal(t, 1, val)

	val, err = cache2.Read(ctx, []byte("key"))
	assert.NoError(t, err)
	assert.Equal(t, 2, val)

	err = i.Invalidate()
	assert.NoError(t, err)
	time.Sleep(time.Millisecond)

	_, err = cache1.Read(ctx, []byte("key"))
	assert.True(t, errors.Is(err, cache.ErrExpiredCacheItem))

	_, err = cache2.Read(ctx, []byte("key"))
	assert.True(t, errors.Is(err, cache.ErrExpiredCacheItem))

	err = i.Invalidate()
	assert.Error(t, err) // already invalidated
}
