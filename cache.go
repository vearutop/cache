package cache

import (
	"context"
	"errors"
	"github.com/swaggest/usecase/status"
	"io"
	"time"
)

var (
	// ErrExpiredCacheItem indicates expired cache entry.
	ErrExpiredCacheItem = errors.New("expired cache item")
	// ErrCacheItemNotFound indicates missing cache entry.
	ErrCacheItemNotFound = status.Wrap(errors.New("missing cache item"), status.NotFound)
)

// DefaultTTL indicates default (unlimited ttl) value for entry expiration time.
const DefaultTTL = time.Duration(0)

// SkipWriteTTL is a ttl value to indicate that cache must not be stored.
const SkipWriteTTL = time.Duration(-1)

// Reader reads from cache.
type Reader interface {
	// Read returns cached value and/or error.
	// if ErrExpiredCacheItem is returned, expired cache value must be returned as well.
	Read(ctx context.Context, key string) (interface{}, error)
}

// Writer writes to cache.
type Writer interface {
	// Write stores value in cache with a given key.
	Write(ctx context.Context, key string, value interface{}) error
}

// ReadWriter reads from and writes to cache.
type ReadWriter interface {
	Reader
	Writer
}

type Entry interface {
	Value() interface{}
}

type Expirable interface {
	ExpireAt() time.Time
}

// Walker calls function for every entry in cache and fails on first error returned by that function.
//
// Count of processed entries is returned.
type Walker interface {
	Walk(func(key string, entry Entry) error) (int, error)
}

// Dumper dumps cache entries in binary format.
type Dumper interface {
	Dump(w io.Writer) (int, error)
}

// Restorer restores cache entries from binary dump.
type Restorer interface {
	Restore(r io.Reader) (int, error)
}

// ErrExpired defines an expiration error with entry details.
type ErrExpired interface {
	error
	Value() interface{}
	ExpiredAt() time.Time
}
