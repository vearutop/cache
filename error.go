package cache

// SentinelError is an error.
type SentinelError string

const (
	// ErrExpiredCacheItem indicates expired cache entry.
	ErrExpiredCacheItem = SentinelError("expired cache item")

	// ErrCacheItemNotFound indicates missing cache entry.
	ErrCacheItemNotFound = SentinelError("missing cache item")

	ErrNothingToInvalidate = SentinelError("nothing to invalidate")

	ErrAlreadyInvalidated = SentinelError("already invalidated")
)

// Error implements error.
func (e SentinelError) Error() string {
	return string(e)
}
