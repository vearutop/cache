package cache

import (
	"context"
	"time"
)

type (
	skipReadCtxKey struct{}
	ttlCtxKey      struct{}
)

func WithTTL(ctx context.Context, ttl time.Duration) context.Context {
	return context.WithValue(ctx, ttlCtxKey{}, ttl)
}

func TTL(ctx context.Context) time.Duration {
	ttl, _ := ctx.Value(ttlCtxKey{}).(time.Duration)
	return ttl
}

// WithSkipRead returns context with cache read ignored.
//
// With such context cache.Reader should always return ErrCacheItemNotFound discarding cached value.
func WithSkipRead(ctx context.Context) context.Context {
	return context.WithValue(ctx, skipReadCtxKey{}, true)
}

// SkipRead returns true if cache read is ignored in context.
func SkipRead(ctx context.Context) bool {
	_, ok := ctx.Value(skipReadCtxKey{}).(bool)
	return ok
}
