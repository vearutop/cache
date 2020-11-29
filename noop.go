package cache

import (
	"context"
)

// NoOp is a ReadWriter stub.
type NoOp struct{}

var _ ReadWriter = NoOp{}

// Read does not find anything.
func (NoOp) Read(ctx context.Context, key string) (interface{}, error) {
	return nil, ErrCacheItemNotFound
}

// Write discards value.
func (NoOp) Write(ctx context.Context, key string, v interface{}) error {
	return nil
}
