package cache

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMemory_evictHeapInuse(t *testing.T) {
	m := NewMemory(MemoryConfig{
		HeapInUseSoftLimit: 1, // Setting heap threshold to 1B to force eviction.
	})

	expire := time.Now().Add(time.Hour)

	// Filling cache with enough items.
	for i := 0; i < 1000; i++ {
		m.data[strconv.Itoa(i)] = entry{
			Val: i,
			Exp: expire.Add(time.Duration(i) * time.Second),
		}
	}

	// Keys 0-99 should be evicted by 0.1 fraction, keys 100-999 should remain.
	m.evictHeapInUse()
	assert.Len(t, m.data, 900)

	for i := 0; i < 100; i++ {
		_, err := m.Read(context.Background(), strconv.Itoa(i))
		assert.EqualError(t, err, ErrCacheItemNotFound.Error())
	}

	for i := 100; i < 1000; i++ {
		_, err := m.Read(context.Background(), strconv.Itoa(i))
		assert.NoError(t, err)
	}
}

func TestMemory_evictHeapInuse_disabled(t *testing.T) {
	m := NewMemory(MemoryConfig{
		HeapInUseSoftLimit: 0, // Setting heap threshold to 0 to disable eviction.
	})

	expire := time.Now().Add(time.Hour)

	// Filling cache with enough items.
	for i := 0; i < 1000; i++ {
		m.data[strconv.Itoa(i)] = entry{
			Val: i,
			Exp: expire.Add(time.Duration(i) * time.Second),
		}
	}

	m.evictHeapInUse()
	assert.Len(t, m.data, 1000)
}

func TestMemory_evictHeapInuse_skipped(t *testing.T) {
	m := NewMemory(MemoryConfig{
		HeapInUseSoftLimit: 1e10, // Setting heap threshold to big value to skip eviction.
	})

	expire := time.Now().Add(time.Hour)

	// Filling cache with enough items.
	for i := 0; i < 1000; i++ {
		m.data[strconv.Itoa(i)] = entry{
			Val: i,
			Exp: expire.Add(time.Duration(i) * time.Second),
		}
	}

	m.evictHeapInUse()
	assert.Len(t, m.data, 1000)
}

func TestMemory_evictHeapInuse_concurrency(t *testing.T) {
	m := NewMemory(MemoryConfig{
		HeapInUseSoftLimit: 1, // Setting heap threshold to 1B value to force eviction.
	})

	ctx := context.Background()
	wg := sync.WaitGroup{}
	wg.Add(1000)

	for i := 0; i < 1000; i++ {
		i := i

		go func() {
			defer wg.Done()

			if i%100 == 0 {
				m.evictHeapInUse()
			}

			k := strconv.Itoa(i % 100)

			err := m.Write(ctx, k, i)
			assert.NoError(t, err)
		}()
	}

	wg.Wait()
}
