package cache_test

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/bool64/cache"
)

// Benchmark_Failover_noSyncRead-8   	 7716646	       148.8 ns/op	       0 B/op	       0 allocs/op.
func Benchmark_Failover_noSyncRead(b *testing.B) {
	c := cache.NewFailover(cache.FailoverConfig{
		SyncRead: false,
	})
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	k := make([]byte, 0, 10)

	for i := 0; i < b.N; i++ {
		k = append(k[:0], []byte("oneone1234")...)
		binary.BigEndian.PutUint32(k[6:], uint32(i%10000))
		_, _ = c.Get(ctx, k, func(ctx context.Context) (interface{}, error) {
			return 123, nil
		})
	}
}

// Benchmark_FailoverSyncRead-8   	 3764518	       321.5 ns/op	     113 B/op	       2 allocs/op.
func Benchmark_FailoverSyncRead(b *testing.B) {
	c := cache.NewFailover(cache.FailoverConfig{
		SyncRead: true,
	})
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	k := make([]byte, 0, 10)

	for i := 0; i < b.N; i++ {
		k = append(k[:0], []byte("oneone1234")...)
		binary.BigEndian.PutUint32(k[6:], uint32(i%10000))

		_, _ = c.Get(ctx, k, func(ctx context.Context) (interface{}, error) {
			return 123, nil
		})
	}
}

// Benchmark_FailoverAlwaysBuild-8   	  869260	      1573 ns/op	     607 B/op	      15 allocs/op.
func Benchmark_FailoverAlwaysBuild(b *testing.B) {
	c := cache.NewFailover(cache.FailoverConfig{})
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()

	k := make([]byte, 0, 10)

	for i := 0; i < b.N; i++ {
		k = append(k[:0], []byte("oneone1234")...)
		binary.BigEndian.PutUint32(k[6:], uint32(i%10000))

		_, _ = c.Get(cache.WithTTL(ctx, -1, false), k, func(ctx context.Context) (interface{}, error) {
			return 123, nil
		})
	}
}
