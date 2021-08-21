package cachex_test

import (
	"context"
	"fmt"
	"github.com/puzpuzpuz/xsync"
	"math"
	"runtime"
	"runtime/debug"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/bool64/cache"
)

func Benchmark_concurrent(b *testing.B) {
	for _, cardinality := range []int{1e6} {
		cardinality := cardinality

		for _, numRoutines := range []int{ /*1, */ runtime.GOMAXPROCS(0)} {
			numRoutines := numRoutines
			for _, writePercent := range []float64{10} {
				writeEvery := int(100.0 / writePercent)

				for _, loader := range []cacheLoader{
					//failoverShardedMap{writeEvery: writeEvery},
					//shardedMap{writeEvery: writeEvery},
					syncMap{writeEvery: writeEvery},
					xSyncMap{writeEvery: writeEvery},
				} {
					loader := loader

					b.Run(fmt.Sprintf("c%d:g%d:w%.2f%%:%T", cardinality, numRoutines, writePercent, loader), func(b *testing.B) {
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

type failoverShardedMap struct {
	c           *cache.Failover
	cardinality int
	writeEvery  int
}

func (sbm failoverShardedMap) make(b *testing.B, cardinality int) cacheLoader {
	b.Helper()

	u := cache.NewShardedMap()
	ctx := context.Background()
	c := cache.NewFailover(func(cfg *cache.FailoverConfig) {
		cfg.Backend = u
	})
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

	return failoverShardedMap{
		c:           c,
		cardinality: cardinality,
	}
}

func (sbm failoverShardedMap) run(b *testing.B, cnt int) {
	b.Helper()

	ctx := context.Background()
	buf := make([]byte, 0, 10)

	w := 0
	for i := 0; i < cnt; i++ {
		i := (i ^ 12345) % sbm.cardinality

		buf = append(buf[:0], []byte(keyPrefix)...)
		buf = append(buf, []byte(strconv.Itoa(i))...)

		w++
		if w == sbm.writeEvery {
			w = 0
			v, err := sbm.c.Get(cache.WithSkipRead(ctx), buf, func(ctx context.Context) (interface{}, error) {
				return smallCachedValue{}, nil
			})

			if v.(smallCachedValue).i != i || err != nil {
				b.Fail()
			}

			continue
		}

		v, err := sbm.c.Get(ctx, buf, func(ctx context.Context) (interface{}, error) {
			return smallCachedValue{}, nil
		})

		if v.(smallCachedValue).i != i || err != nil {
			b.Fail()
		}
	}
}

type shardedMap struct {
	c           *cache.ShardedMap
	cardinality int
	writeEvery  int
}

func (sbm shardedMap) make(b *testing.B, cardinality int) cacheLoader {
	b.Helper()

	ctx := context.Background()
	c := cache.NewShardedMap()
	buf := make([]byte, 0)

	for i := 0; i < cardinality; i++ {
		i := i

		buf = append(buf[:0], []byte(keyPrefix)...)
		buf = append(buf, []byte(strconv.Itoa(i))...)

		err := c.Write(ctx, buf, makeCachedValue(i))
		if err != nil {
			b.Fail()
		}
	}

	return shardedMap{
		c:           c,
		cardinality: cardinality,
	}
}

func (sbm shardedMap) run(b *testing.B, cnt int) {
	b.Helper()

	ctx := context.Background()
	buf := make([]byte, 0, 10)

	w := 0
	for i := 0; i < cnt; i++ {
		i := (i ^ 12345) % sbm.cardinality

		buf = append(buf[:0], []byte(keyPrefix)...)
		buf = append(buf, []byte(strconv.Itoa(i))...)

		w++
		if w == sbm.writeEvery {
			w = 0
			err := sbm.c.Write(ctx, buf, makeCachedValue(i))

			if err != nil {
				b.Fail()
			}

			continue
		}

		v, err := sbm.c.Read(ctx, buf)

		if v.(smallCachedValue).i != i || err != nil {
			b.Fail()
		}
	}
}

type syncMap struct {
	c           *sync.Map
	cardinality int
	writeEvery  int
}

func (sbm syncMap) make(b *testing.B, cardinality int) cacheLoader {
	b.Helper()

	c := sync.Map{}
	buf := make([]byte, 0)

	for i := 0; i < cardinality; i++ {
		i := i

		buf = append(buf[:0], []byte(keyPrefix)...)
		buf = append(buf, []byte(strconv.Itoa(i))...)

		c.Store(string(buf), makeCachedValue(i))
	}

	return syncMap{
		c:           &c,
		cardinality: cardinality,
	}
}

func (sbm syncMap) run(b *testing.B, cnt int) {
	b.Helper()

	buf := make([]byte, 0, 10)

	w := 0
	for i := 0; i < cnt; i++ {
		i := (i ^ 12345) % sbm.cardinality

		buf = append(buf[:0], []byte(keyPrefix)...)
		buf = append(buf, []byte(strconv.Itoa(i))...)

		w++
		if w == sbm.writeEvery {
			w = 0
			sbm.c.Store(string(buf), makeCachedValue(i))
			continue
		}

		v, ok := sbm.c.Load(string(buf))

		if !ok || v.(smallCachedValue).i != i {
			b.Fail()
		}
	}
}

type xSyncMap struct {
	c           *xsync.Map
	cardinality int
	writeEvery  int
}

func (sbm xSyncMap) make(b *testing.B, cardinality int) cacheLoader {
	b.Helper()

	c := xsync.Map{}
	buf := make([]byte, 0)

	for i := 0; i < cardinality; i++ {
		i := i

		buf = append(buf[:0], []byte(keyPrefix)...)
		buf = append(buf, []byte(strconv.Itoa(i))...)

		c.Store(string(buf), makeCachedValue(i))
	}

	return xSyncMap{
		c:           &c,
		cardinality: cardinality,
	}
}

func (sbm xSyncMap) run(b *testing.B, cnt int) {
	b.Helper()

	buf := make([]byte, 0, 10)

	w := 0
	for i := 0; i < cnt; i++ {
		i := (i ^ 12345) % sbm.cardinality

		buf = append(buf[:0], []byte(keyPrefix)...)
		buf = append(buf, []byte(strconv.Itoa(i))...)

		w++
		if w == sbm.writeEvery {
			w = 0
			sbm.c.Store(string(buf), makeCachedValue(i))
			continue
		}

		v, ok := sbm.c.Load(string(buf))

		if !ok || v.(smallCachedValue).i != i {
			b.Fail()
		}
	}
}
