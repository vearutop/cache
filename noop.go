package cache

import (
	"context"
)

// NoOp is a ReadWriter stub.
type NoOp struct{}

var _ ReadWriter = NoOp{}

// Write discards value.
func (NoOp) Write(_ context.Context, _ []byte, _ interface{}) error {
	return nil
}

// Read does not find anything.
func (NoOp) Read(_ context.Context, _ []byte) (interface{}, error) {
	return nil, ErrCacheItemNotFound
}
