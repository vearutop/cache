// Package cache defines caching contract and provides in-rwMutexMap implementation.
// Focused on resilient and race-free operation on top of external sources.
//
// Features:
//
//  - High level of control with rich configuration.
//  - No external dependencies.
//  - Separated expiration and janitor allows usage of stale values to protect from upstream errors and racy updates.
//  - Background janitor does not affect sync performance.
//  - Build errors are cached with low TTL to avoid flooding unhealthy upstream.
//  - Background update of expired cache value improves performance.
//  - Cache updates are locked per key to eliminate racy updates and only update once.
//  - Allows logging, stats collection.
//  - Propagates context to allow better control of backend and application components.
//  - Allows mass expiration and removal (drop cache).
//  - Expiration jitter to avoid massive synchronized expiration.
package cache
