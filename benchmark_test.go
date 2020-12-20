package cache_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/bool64/cache"
	pca "github.com/patrickmn/go-cache"
)

func Benchmark_Memory(b *testing.B) {
	c := cache.NewMemory()
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		k := "oneone" + strconv.Itoa(i%10000)
		// nolint
		if i < 10000 {
			_ = c.Write(ctx, k, 123)
		}
		// nolint
		_, _ = c.Read(ctx, k)
	}
}

func Benchmark_SyncMap(b *testing.B) {
	c := cache.NewSyncMap()
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		k := "oneone" + strconv.Itoa(i%10000)
		// nolint
		if i < 10000 {
			_ = c.Write(ctx, k, 123)
		}
		// nolint
		_, _ = c.Read(ctx, k)
	}
}

func Benchmark_Failover(b *testing.B) {
	c := cache.NewFailover(cache.FailoverConfig{})
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		k := "oneone" + strconv.Itoa(i%10000)
		// nolint
		_, _ = c.Get(ctx, k, func(ctx context.Context) (interface{}, error) {
			return 123, nil
		})
	}
}

func Benchmark_FailoverSyncRead(b *testing.B) {
	c := cache.NewFailover(cache.FailoverConfig{
		SyncRead: true,
	})
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		k := "oneone" + strconv.Itoa(i%10000)
		// nolint
		_, _ = c.Get(ctx, k, func(ctx context.Context) (interface{}, error) {
			return 123, nil
		})
	}
}

// Benchmark_StringKeyPatrickmn is archived.
// Add import `pca "github.com/patrickmn/go-cache"` to enable it.
// Sample result:
// Benchmark_Memory-16                 	 6299344	       180 ns/op	      16 B/op	       1 allocs/op
// Benchmark_Failover-16               	 5991889	       215 ns/op	      16 B/op	       1 allocs/op
// Benchmark_FailoverSyncRead-16       	 3199423	       355 ns/op	     113 B/op	       3 allocs/op
// Benchmark_FailoverAlwaysBuild-16    	 1000000	      1134 ns/op	     523 B/op	       7 allocs/op
// Benchmark_Patrickmn-4          	     5000000	       258 ns/op	      16 B/op	       1 allocs/op
//*
func Benchmark_Patrickmn(b *testing.B) {
	c := pca.New(5*time.Minute, 10*time.Minute)

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		k := "oneone" + strconv.Itoa(i%10000)

		if i < 10000 {
			c.Set(k, 123, time.Minute)
		}

		_, _ = c.Get(k)
	}
}

//*/

func Benchmark_FailoverAlwaysBuild(b *testing.B) {
	c := cache.NewFailover(cache.FailoverConfig{})
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		k := "oneone" + strconv.Itoa(i)
		// nolint
		_, _ = c.Get(ctx, k, func(ctx context.Context) (interface{}, error) {
			return 123, nil
		})
	}
}
