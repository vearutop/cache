package cache_test

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/bool64/cache"
	"github.com/dgraph-io/ristretto"
	pca "github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/require"
)

func Benchmark_Failover_SyncMap_concurrent(b *testing.B) {
	u := cache.NewSyncMap()
	ctx := context.Background()
	c := cache.NewFailover(cache.FailoverConfig{
		Upstream: u,
	})

	cardinality := 10000
	for i := 0; i < cardinality; i++ {
		k := "oneone" + strconv.Itoa(i)

		_, err := c.Get(ctx, k, func(ctx context.Context) (interface{}, error) {
			return 123, nil
		})
		if err != nil {
			b.Fail()
		}
	}

	b.ReportAllocs()
	b.ResetTimer()

	numRoutines := 50
	wg := sync.WaitGroup{}
	wg.Add(numRoutines)

	for r := 0; r < numRoutines; r++ {
		cnt := b.N / numRoutines
		if r == 0 {
			cnt = b.N - cnt*(numRoutines-1)
		}

		go func() {
			for i := 0; i < cnt; i++ {
				k := "oneone" + strconv.Itoa((i^12345)%cardinality)
				v, err := c.Get(ctx, k, func(ctx context.Context) (interface{}, error) {
					return 456, nil
				})

				if v.(int) != 123 || err != nil {
					b.Fail()
				}
			}
			wg.Done()
		}()
	}

	wg.Wait()
}

func Benchmark_FailoverByte_ShardedByteMap_concurrent(b *testing.B) {
	u := cache.NewShardedByteMap()
	ctx := context.Background()
	c := cache.NewFailoverByte(cache.FailoverByteConfig{
		Upstream: u,
	})

	before := heapInUse()

	cardinality := 100000
	buf := make([]byte, 0, 10)

	for i := 0; i < cardinality; i++ {
		// k := "oneone" + strconv.Itoa(i)
		buf = append(buf[:0], []byte("oneone")...)
		buf = append(buf, []byte(strconv.Itoa(i))...)

		_, err := c.Get(ctx, buf, func(ctx context.Context) (interface{}, error) {
			return 123, nil
		})
		if err != nil {
			b.Fail()
		}
	}

	b.ReportAllocs()
	b.ResetTimer()

	numRoutines := 50
	wg := sync.WaitGroup{}
	wg.Add(numRoutines)

	for r := 0; r < numRoutines; r++ {
		cnt := b.N / numRoutines
		if r == 0 {
			cnt = b.N - cnt*(numRoutines-1)
		}

		go func() {
			buf := make([]byte, 0, 10)

			for i := 0; i < cnt; i++ {
				// k := "oneone" + strconv.Itoa((i^12345)%cardinality)
				buf = append(buf[:0], []byte("oneone")...)
				buf = append(buf, []byte(strconv.Itoa((i^12345)%cardinality))...)

				// buf = []byte("oneone" + strconv.Itoa((i^12345)%cardinality))

				v, err := c.Get(ctx, buf, func(ctx context.Context) (interface{}, error) {
					return 456, nil
				})

				if v.(int) != 123 || err != nil {
					b.Fail()
				}
			}
			wg.Done()
		}()
	}

	wg.Wait()

	b.StopTimer()

	b.ReportMetric(float64(heapInUse()-before)/(1024*1024), "MB/inuse")

	c.Get(context.Background(), []byte(`aaa`), func(ctx context.Context) (interface{}, error) {
		return 123, nil
	})
}

func Benchmark_SyncMap_concurrent(b *testing.B) {
	c := cache.NewSyncMap()
	ctx := context.Background()

	cardinality := 10000
	for i := 0; i < cardinality; i++ {
		k := "oneone" + strconv.Itoa(i)
		_ = c.Write(ctx, k, 123)
	}

	b.ReportAllocs()
	b.ResetTimer()

	numRoutines := 50
	wg := sync.WaitGroup{}
	wg.Add(numRoutines)

	for r := 0; r < numRoutines; r++ {
		cnt := b.N / numRoutines
		if r == 0 {
			cnt = b.N - cnt*(numRoutines-1)
		}

		go func() {
			for i := 0; i < cnt; i++ {
				k := "oneone" + strconv.Itoa((i^12345)%cardinality)
				v, _ := c.Read(ctx, k)

				if v.(int) != 123 {
					b.Fail()
				}
			}
			wg.Done()
		}()
	}

	wg.Wait()
}

func Benchmark_RWMutexMap_concurrent(b *testing.B) {
	c := cache.NewRWMutexMap()
	ctx := context.Background()

	cardinality := 10000
	for i := 0; i < cardinality; i++ {
		k := "oneone" + strconv.Itoa(i)
		_ = c.Write(ctx, k, 123)
	}

	b.ReportAllocs()
	b.ResetTimer()

	numRoutines := 50
	wg := sync.WaitGroup{}
	wg.Add(numRoutines)

	for r := 0; r < numRoutines; r++ {
		cnt := b.N / numRoutines
		if r == 0 {
			cnt = b.N - cnt*(numRoutines-1)
		}

		go func() {
			for i := 0; i < cnt; i++ {
				k := "oneone" + strconv.Itoa((i^12345)%cardinality)
				v, _ := c.Read(ctx, k)

				if v.(int) != 123 {
					b.Fail()
				}
			}
			wg.Done()
		}()
	}

	wg.Wait()
}

func Benchmark_Patrickmn_concurrent(b *testing.B) {
	before := heapInUse()

	c := pca.New(5*time.Minute, 10*time.Minute)

	cardinality := 10000
	for i := 0; i < cardinality; i++ {
		k := "oneone" + strconv.Itoa(i)
		c.Set(k, 123, time.Minute)
	}

	b.ReportAllocs()
	b.ResetTimer()

	numRoutines := 50
	wg := sync.WaitGroup{}
	wg.Add(numRoutines)

	for r := 0; r < numRoutines; r++ {
		cnt := b.N / numRoutines
		if r == 0 {
			cnt = b.N - cnt*(numRoutines-1)
		}

		go func() {
			for i := 0; i < cnt; i++ {
				k := "oneone" + strconv.Itoa((i^12345)%cardinality)
				v, _ := c.Get(k)

				if v.(int) != 123 {
					b.Fail()
				}
			}
			wg.Done()
		}()
	}

	wg.Wait()
	b.StopTimer()

	b.ReportMetric(float64(heapInUse()-before)/(1024*1024), "MB/inuse")

	fmt.Sprintln(c)
}

func Benchmark_Ristretto_concurrent(b *testing.B) {
	cardinality := 100000
	c, err := ristretto.NewCache(&ristretto.Config{
		NumCounters: int64(10 * cardinality),
		MaxCost:     int64(10 * cardinality),
		BufferItems: 64,
	})
	require.NoError(b, err)

	buf := make([]byte, 0, 10)

	before := heapInUse()

	for i := 0; i < cardinality; i++ {
		// k := "oneone" + strconv.Itoa(i)
		buf = append(buf[:0], []byte("oneone")...)
		buf = append(buf, []byte(strconv.Itoa(i))...)
		require.True(b, c.Set(buf, 123, 1))
	}

	b.ReportAllocs()
	b.ResetTimer()

	numRoutines := 50
	wg := sync.WaitGroup{}
	wg.Add(numRoutines)

	for r := 0; r < numRoutines; r++ {
		cnt := b.N / numRoutines
		if r == 0 {
			cnt = b.N - cnt*(numRoutines-1)
		}

		go func() {
			buf := make([]byte, 0, 10)

			for i := 0; i < cnt; i++ {
				// k := "oneone" + strconv.Itoa((i^12345)%cardinality)
				buf = append(buf[:0], []byte("oneone")...)
				buf = append(buf, []byte(strconv.Itoa((i^12345)%cardinality))...)

				v, _ := c.Get(buf)

				if v.(int) != 123 {
					b.Fail()
				}
			}
			wg.Done()
		}()
	}

	wg.Wait()

	b.StopTimer()

	b.ReportMetric(float64(heapInUse()-before)/(1024*1024), "MB/inuse")

	c.Get("")
}
