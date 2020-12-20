// Package cache defines caching contract and provides in-rwMutexMap implementation.
// Focused on resilient and race-free operation on top of external sources.
//
// Features:
//
//  - High level of control with rich configuration.
//  - No external dependencies.
//  - Separated expiration and cleaner allows usage of stale values to protect from upstream errors and racy updates.
//  - Background cleaner does not affect sync performance.
//  - Upstream errors are cached with low TTL to avoid flooding unhealthy upstream.
//  - Background update of expired cache value improves performance.
//  - Cache updates are locked per key to eliminate racy updates and only update once.
//  - Allows logging, stats collection.
//  - Propagates context to allow better control of upstream and application components.
//  - Allows mass expiration and removal (drop cache).
//  - Expiration jitter to avoid massive synchronized expiration.
//  - Warm cache with background value watcher.
package cache
