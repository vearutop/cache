package cache_test

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"runtime/debug"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/bool64/cache"
	"github.com/dgraph-io/ristretto"
	pca "github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/require"
)

func Benchmark_concurrentRead(b *testing.B) {
	for _, cardinality := range []int{10000} {
		cardinality := cardinality

		for _, numRoutines := range []int{1, 50} {
			numRoutines := numRoutines

			for _, loader := range []cacheLoader{
				failoverShardedByteMap{},
				failoverSyncMap{},
				syncMap{},
				rwMutexMap{},
				patrickmn{},
				rist{},
			} {
				loader := loader

				b.Run(fmt.Sprintf("%d:%d:%T", cardinality, numRoutines, loader), func(b *testing.B) {
					before := heapInUse()

					c := loader.make(b, cardinality)

					b.ReportAllocs()
					b.ResetTimer()

					wg := sync.WaitGroup{}
					wg.Add(numRoutines)

					for r := 0; r < numRoutines; r++ {
						cnt := b.N / numRoutines
						if r == 0 {
							cnt = b.N - cnt*(numRoutines-1)
						}

						go func() {
							c.run(b, cnt)
							wg.Done()
						}()
					}

					wg.Wait()
					b.StopTimer()
					b.ReportMetric(float64(heapInUse()-before)/(1024*1024), "MB/inuse")
					fmt.Sprintln(c)
				})
			}
		}
	}
}

// cachedValue represents a small value for a cached item.
type smallCachedValue struct {
	b bool
	s string
	i int
}

func makeCachedValue(i int) smallCachedValue {
	return smallCachedValue{
		i: i,
		s: longString + strconv.Itoa(i),
		b: true,
	}
}

func init() {
	cache.GobRegister(smallCachedValue{})
}

const (
	longString = "looooooooooooooooooooooooooongstring"
	keyPrefix  = "thekey"
)

type cacheLoader interface {
	make(b *testing.B, cardinality int) cacheLoader
	run(b *testing.B, cnt int)
}

////////

type failoverShardedByteMap struct {
	c           *cache.FailoverByte
	cardinality int
}

func (sbm failoverShardedByteMap) make(b *testing.B, cardinality int) cacheLoader {
	b.Helper()

	u := cache.NewShardedByteMap()
	ctx := context.Background()
	c := cache.NewFailoverByte(cache.FailoverByteConfig{Upstream: u})
	buf := make([]byte, 0)

	for i := 0; i < cardinality; i++ {
		i := i

		buf = append(buf[:0], []byte(keyPrefix)...)
		buf = append(buf, []byte(strconv.Itoa(i))...)

		_, err := c.Get(ctx, buf, func(ctx context.Context) (interface{}, error) {
			return makeCachedValue(i), nil
		})
		if err != nil {
			b.Fail()
		}
	}

	return failoverShardedByteMap{
		c:           c,
		cardinality: cardinality,
	}
}

func (sbm failoverShardedByteMap) run(b *testing.B, cnt int) {
	b.Helper()

	ctx := context.Background()
	buf := make([]byte, 0, 10)

	for i := 0; i < cnt; i++ {
		i := (i ^ 12345) % sbm.cardinality

		buf = append(buf[:0], []byte(keyPrefix)...)
		buf = append(buf, []byte(strconv.Itoa(i))...)

		v, err := sbm.c.Get(ctx, buf, func(ctx context.Context) (interface{}, error) {
			return smallCachedValue{}, nil
		})

		if v.(smallCachedValue).i != i || err != nil {
			b.Fail()
		}
	}
}

type failoverSyncMap struct {
	c           *cache.Failover
	cardinality int
}

func (sbm failoverSyncMap) make(b *testing.B, cardinality int) cacheLoader {
	b.Helper()

	u := cache.NewSyncMap()
	ctx := context.Background()
	c := cache.NewFailover(cache.FailoverConfig{Upstream: u})

	for i := 0; i < cardinality; i++ {
		i := i
		k := keyPrefix + strconv.Itoa(i)

		_, err := c.Get(ctx, k, func(ctx context.Context) (interface{}, error) {
			return makeCachedValue(i), nil
		})
		if err != nil {
			b.Fail()
		}
	}

	return failoverSyncMap{
		c:           c,
		cardinality: cardinality,
	}
}

func (sbm failoverSyncMap) run(b *testing.B, cnt int) {
	b.Helper()

	ctx := context.Background()

	for i := 0; i < cnt; i++ {
		i := (i ^ 12345) % sbm.cardinality
		k := keyPrefix + strconv.Itoa(i)

		v, err := sbm.c.Get(ctx, k, func(ctx context.Context) (interface{}, error) {
			return smallCachedValue{}, nil
		})

		if v.(smallCachedValue).i != i || err != nil {
			b.Fail()
		}
	}
}

type syncMap struct {
	c           *cache.SyncMap
	cardinality int
}

func (sbm syncMap) make(b *testing.B, cardinality int) cacheLoader {
	b.Helper()

	ctx := context.Background()
	c := cache.NewSyncMap()

	for i := 0; i < cardinality; i++ {
		k := keyPrefix + strconv.Itoa(i)

		err := c.Write(ctx, k, makeCachedValue(i))
		if err != nil {
			b.Fail()
		}
	}

	return syncMap{
		c:           c,
		cardinality: cardinality,
	}
}

func (sbm syncMap) run(b *testing.B, cnt int) {
	b.Helper()

	ctx := context.Background()

	for i := 0; i < cnt; i++ {
		i := (i ^ 12345) % sbm.cardinality
		k := keyPrefix + strconv.Itoa(i)

		v, err := sbm.c.Read(ctx, k)

		if v.(smallCachedValue).i != i || err != nil {
			b.Fail()
		}
	}
}

type rwMutexMap struct {
	c           *cache.RWMutexMap
	cardinality int
}

func (sbm rwMutexMap) make(b *testing.B, cardinality int) cacheLoader {
	b.Helper()

	ctx := context.Background()
	c := cache.NewRWMutexMap()

	for i := 0; i < cardinality; i++ {
		k := keyPrefix + strconv.Itoa(i)

		err := c.Write(ctx, k, makeCachedValue(i))
		if err != nil {
			b.Fail()
		}
	}

	return rwMutexMap{
		c:           c,
		cardinality: cardinality,
	}
}

func (sbm rwMutexMap) run(b *testing.B, cnt int) {
	b.Helper()

	ctx := context.Background()

	for i := 0; i < cnt; i++ {
		i := (i ^ 12345) % sbm.cardinality
		k := keyPrefix + strconv.Itoa(i)

		v, err := sbm.c.Read(ctx, k)

		if v.(smallCachedValue).i != i || err != nil {
			b.Fail()
		}
	}
}

type patrickmn struct {
	c           *pca.Cache
	cardinality int
}

func (sbm patrickmn) make(b *testing.B, cardinality int) cacheLoader {
	b.Helper()

	c := pca.New(5*time.Minute, 10*time.Minute)

	for i := 0; i < cardinality; i++ {
		c.Set(keyPrefix+strconv.Itoa(i), makeCachedValue(i), time.Hour)
	}

	return patrickmn{
		c:           c,
		cardinality: cardinality,
	}
}

func (sbm patrickmn) run(b *testing.B, cnt int) {
	b.Helper()

	for i := 0; i < cnt; i++ {
		i := (i ^ 12345) % sbm.cardinality

		v, found := sbm.c.Get(keyPrefix + strconv.Itoa(i))

		if v.(smallCachedValue).i != i || !found {
			b.Fail()
		}
	}
}

type rist struct {
	c           *ristretto.Cache
	cardinality int
}

func (sbm rist) make(b *testing.B, cardinality int) cacheLoader {
	b.Helper()

	c, err := ristretto.NewCache(&ristretto.Config{
		NumCounters: int64(10 * cardinality),
		MaxCost:     int64(10 * cardinality),
		BufferItems: 64,
	})
	require.NoError(b, err)

	for i := 0; i < cardinality; i++ {
		c.Set(keyPrefix+strconv.Itoa(i), makeCachedValue(i), 1)
	}

	return rist{
		c:           c,
		cardinality: cardinality,
	}
}

func (sbm rist) run(b *testing.B, cnt int) {
	b.Helper()

	for i := 0; i < cnt; i++ {
		i := (i ^ 12345) % sbm.cardinality

		k := keyPrefix + strconv.Itoa(i)
		v, found := sbm.c.Get(k)

		if !found {
			b.Fatalf("key not found: %s", k)
		}

		if sm, ok := v.(smallCachedValue); ok && sm.i != i {
			b.Fatalf("value not equal %s %d %d", k, sm.i, i)
		}
	}
}

func Benchmark_MutexMap_concurrent(b *testing.B) {
	c := cache.NewMutexMap()
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

func Benchmark_ShardedMap_concurrent(b *testing.B) {
	c := cache.NewShardedMap()
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

func Benchmark_ShardedByteMap_concurrent(b *testing.B) {
	c := cache.NewShardedByteMap()
	ctx := context.Background()

	cardinality := 10000
	for i := 0; i < cardinality; i++ {
		k := "oneone" + strconv.Itoa(i)
		_ = c.Write(ctx, []byte(k), 123)
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
				v, _ := c.Read(ctx, []byte(k))

				if v.(int) != 123 {
					b.Fail()
				}
			}
			wg.Done()
		}()
	}

	wg.Wait()
}

func heapInUse() uint64 {
	var (
		m         = runtime.MemStats{}
		prevInUse uint64
	)

	for {
		runtime.ReadMemStats(&m)

		if math.Abs(float64(m.HeapInuse-prevInUse)) < 1*1024 {
			break
		}

		prevInUse = m.HeapInuse

		time.Sleep(50 * time.Millisecond)
		runtime.GC()
		debug.FreeOSMemory()
	}

	return m.HeapInuse
}
