package cache

import (
	"context"
	"runtime"
	"sort"
	"time"
)

func (c *memory) evictHeapInUse() {
	if c.config.HeapInUseSoftLimit == 0 {
		return
	}

	runtime.GC()

	m := runtime.MemStats{}
	runtime.ReadMemStats(&m)

	if m.HeapInuse < c.config.HeapInUseSoftLimit {
		return
	}

	type entry struct {
		key      string
		expireAt time.Time
	}

	c.RLock()
	keysCnt := len(c.data)
	c.RUnlock()

	entries := make([]entry, 0, keysCnt)

	// Collect all keys and expirations.
	c.RLock()
	for k, i := range c.data {
		entries = append(entries, entry{key: k, expireAt: i.Exp})
	}
	c.RUnlock()

	// Sort entries to put most expired in head.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].expireAt.Before(entries[j].expireAt)
	})

	evictFraction := c.config.HeapInUseEvictFraction
	if evictFraction == 0 {
		evictFraction = 0.1
	}

	evictItems := int(float64(len(entries)) * evictFraction)

	if c.stat != nil {
		c.stat.Add(context.Background(), MetricEvict, float64(evictItems), "name", c.config.Name)
	}

	for i := 0; i < evictItems; i++ {
		c.Lock()
		delete(c.data, entries[i].key)
		c.Unlock()
	}
}
