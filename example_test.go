package cache_test

import (
	"context"
	"fmt"
	"time"

	"github.com/bool64/cache"
	"github.com/bool64/ctxd"
	"github.com/bool64/stats"
)

func ExampleNewMemory() {
	// Create cache instance.
	c := cache.NewMemory(cache.MemoryConfig{
		Name:       "dogs",
		TimeToLive: 13 * time.Minute,
		Logger:     &ctxd.LoggerMock{},
		Stats:      &stats.TrackerMock{},

		// Tweak these parameters to reduce/stabilize memory consumption at cost of cache hit rate.
		// If cache cardinality and size are reasonable, default values should be fine.
		DeleteExpiredAfter:       time.Hour,
		DeleteExpiredJobInterval: 10 * time.Minute,
		HeapInUseSoftLimit:       200 * 1024 * 1024, // 200MB soft limit for process heap in use.
		HeapInUseEvictFraction:   0.2,               // Drop 20% of mostly expired items (including non-expired) on heap overuse.
	})

	// Use context if available.
	ctx := context.TODO()

	// Write value to cache.
	_ = c.Write(ctx, "my-key", []int{1, 2, 3})

	// Read value from cache.
	val, _ := c.Read(ctx, "my-key")
	fmt.Printf("%v", val)

	// Output:
	// [1 2 3]
}
