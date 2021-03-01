package cache_test

import (
	"context"
	"testing"

	"github.com/bool64/cache"
	"github.com/stretchr/testify/assert"
)

func TestNoOp_Read(t *testing.T) {
	v, err := cache.NoOp{}.Read(context.Background(), "foo")
	assert.Nil(t, v)
	assert.EqualError(t, err, "not found: missing cache item")
}

func TestNoOp_Write(t *testing.T) {
	err := cache.NoOp{}.Write(context.Background(), "foo", 123)
	assert.NoError(t, err)

	v, err := cache.NoOp{}.Read(context.Background(), "foo")
	assert.Nil(t, v)
	assert.EqualError(t, err, "not found: missing cache item")
}
