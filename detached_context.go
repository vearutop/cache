package cache

import (
	"context"
	"time"
)

type detachedContext struct {
	ctx context.Context
}

func (dctx detachedContext) Deadline() (deadline time.Time, ok bool) {
	return time.Time{}, false
}

func (dctx detachedContext) Done() <-chan struct{} {
	return nil
}

func (dctx detachedContext) Err() error {
	return nil
}

func (dctx detachedContext) Value(key interface{}) interface{} {
	return dctx.ctx.Value(key)
}
