package cache_test

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/VictoriaMetrics/fastcache"
	"github.com/bool64/cache"
	pca "github.com/patrickmn/go-cache"
)

func Benchmark_Memory(b *testing.B) {
	c := cache.NewRWMutexMap()
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

//Benchmark_SyncMap-8   	 5788734	       227.6 ns/op	      16 B/op	       1 allocs/op
//Benchmark_SyncMap-8   	 5577684	       219.8 ns/op	      16 B/op	       1 allocs/op
//Benchmark_SyncMap-8   	 5626167	       218.9 ns/op	      16 B/op	       1 allocs/op
//Benchmark_SyncMap-8   	 5830920	       219.4 ns/op	      16 B/op	       1 allocs/op
//Benchmark_SyncMap-8   	 5781390	       203.9 ns/op	      16 B/op	       1 allocs/op
//Benchmark_SyncMap-8   	 5460158	       204.2 ns/op	      16 B/op	       1 allocs/op
//Benchmark_SyncMap-8   	 5258498	       205.8 ns/op	      16 B/op	       1 allocs/op
//Benchmark_SyncMap-8   	 5260357	       203.9 ns/op	      16 B/op	       1 allocs/op
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

func BenchmarkCacheSetGet(b *testing.B) {
	const items = 1 << 16
	c := fastcache.New(12 * items)
	defer c.Reset()
	b.ReportAllocs()
	b.SetBytes(2 * items)
	b.RunParallel(func(pb *testing.PB) {
		k := []byte("\x00\x00\x00\x00")
		v := []byte("xyza")
		var buf []byte
		for pb.Next() {
			for i := 0; i < items; i++ {
				k[0]++
				if k[0] == 0 {
					k[1]++
				}
				c.Set(k, v)
			}
			for i := 0; i < items; i++ {
				k[0]++
				if k[0] == 0 {
					k[1]++
				}
				buf = c.Get(buf[:0], k)
				if string(buf) != string(v) {
					panic(fmt.Errorf("BUG: invalid value obtained; got %q; want %q", buf, v))
				}
			}
		}
	})
}
