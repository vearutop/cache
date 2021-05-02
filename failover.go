package cache

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"time"

	"github.com/bool64/ctxd"
	"github.com/bool64/stats"
)

// FailoverConfig is optional configuration for NewFailover.
type FailoverConfig struct {
	// Name is added to logs and stats.
	Name string

	// Backend is a cache instance, ShardedMap created by default.
	Backend ReadWriter

	// BackendConfig is a configuration for ShardedMap cache instance if Backend is not provided.
	BackendConfig MemoryConfig

	// FailedUpdateTTL is ttl of failed build cache, default 20s, -1 disables errors cache.
	FailedUpdateTTL time.Duration

	// UpdateTTL is a time interval to retry update, default 1 minute.
	UpdateTTL time.Duration

	// BackgroundUpdate enables update in background, default is sync update with updated value served.
	BackgroundUpdate bool

	// SyncRead enables backend reading in the critical section to ensure cache miss
	// will not trigger multiple updates sequentially.
	//
	// Probability of such issue is low, there is performance penalty for enabling this option.
	SyncRead bool

	// ObserveMutability enables deep equal check with metric collection on cache update.
	ObserveMutability bool

	// MaxExpiration is duration of stale value fitness.
	// If value has expired longer than this duration it won't be served unless value update failure.
	MaxExpiration time.Duration

	// Logger collects messages with context.
	Logger ctxd.Logger

	// Stats tracks stats.
	Stats stats.Tracker
}

// Failover is a cache frontend to manage cache updates in a non-conflicting and performant way.
//
// Please use NewFailover to create instance.
type Failover struct {
	// Errors caches errors of failed updates.
	Errors *ShardedMap

	backend  ReadWriter
	lock     sync.Mutex               // Securing keyLocks
	keyLocks map[string]chan struct{} // Preventing update concurrency per key
	config   FailoverConfig
	log      ctxd.Logger
	stat     stats.Tracker
}

// NewFailover creates a Failover cache instance.
//
// Build is locked per key to avoid concurrent updates, new value is served .
// Stale value is served during non-concurrent update (up to FailoverConfig.UpdateTTL long).
func NewFailover(config FailoverConfig) *Failover {
	if config.UpdateTTL == 0 {
		config.UpdateTTL = time.Minute
	}

	if config.FailedUpdateTTL == 0 {
		config.FailedUpdateTTL = 20 * time.Second
	}

	sc := &Failover{}
	sc.config = config

	sc.log = config.Logger
	if sc.log == nil {
		sc.log = ctxd.NoOpLogger{}
	}

	sc.stat = config.Stats
	if sc.stat == nil {
		sc.stat = stats.NoOp{}
	}

	sc.backend = config.Backend

	if sc.backend == nil {
		config.BackendConfig.Name = config.Name
		config.BackendConfig.Logger = config.Logger
		config.BackendConfig.Stats = config.Stats
		sc.backend = NewShardedMap(config.BackendConfig)
	}

	if config.FailedUpdateTTL > -1 {
		sc.Errors = NewShardedMap(MemoryConfig{
			Name:       "err_" + config.Name,
			Logger:     config.Logger,
			Stats:      config.Stats,
			TimeToLive: config.FailedUpdateTTL,

			// Short cleanup intervals to avoid storing potentially heavy errors for long time.
			DeleteExpiredAfter:       time.Minute,
			DeleteExpiredJobInterval: time.Minute,
		})
	}

	sc.keyLocks = make(map[string]chan struct{})

	return sc
}

// Get returns value from cache or from build function.
func (sc *Failover) Get(ctx context.Context, key []byte, buildFunc func(ctx context.Context) (interface{}, error)) (interface{}, error) {
	var (
		value interface{}
		err   error
	)

	// Performing initial check before critical section.
	if !sc.config.SyncRead {
		// Checking for valid value in cache store.
		if value, err = sc.backend.Read(ctx, key); err == nil {
			return value, nil
		}
	}

	// Locking key for update or finding active lock.
	sc.lock.Lock()
	var keyLock chan struct{}

	alreadyLocked := false

	keyLock, alreadyLocked = sc.keyLocks[string(key)]
	if !alreadyLocked {
		keyLock = make(chan struct{})
		sc.keyLocks[string(key)] = keyLock
	}
	sc.lock.Unlock()

	// Releasing the lock.
	defer func() {
		if !alreadyLocked {
			sc.lock.Lock()
			delete(sc.keyLocks, string(key))
			close(keyLock)
			sc.lock.Unlock()
		}
	}()

	// Performing initial check in critical section.
	if sc.config.SyncRead {
		// Checking for valid value in cache store.
		if value, err = sc.backend.Read(ctx, key); err == nil {
			return value, nil
		}
	}

	// If already locked waiting for completion before checking backend again.
	if alreadyLocked {
		// Return immediately if update is in progress and stale value available.
		if val, freshEnough := sc.freshEnough(err, value); freshEnough {
			return val, nil
		}

		return sc.waitForValue(ctx, key, keyLock)
	}

	// Pushing expired value with short ttl to serve during update.
	if val, freshEnough := sc.freshEnough(err, value); freshEnough {
		if err = sc.refreshStale(ctx, key, val); err != nil {
			return nil, err
		}
	}

	// Check if update failed recently.
	if err := sc.recentlyFailed(ctx, key); err != nil {
		return nil, err
	}

	// Detaching context into background if FailoverConfig.SyncUpdate is disabled and there is a stale value already.
	ctx, syncUpdate := sc.ctxSync(ctx, err)

	// Running cache build synchronously.
	if syncUpdate {
		updated, err := sc.doBuild(ctx, key, value, buildFunc)
		// Return stale value if update fails.
		if err != nil {
			sc.log.Warn(ctx, "failed to update stale cache value",
				"error", err,
				"name", sc.config.Name,
				"key", key)

			if value != nil {
				return value, nil
			}
		}

		return updated, err
	}

	// Disabling defer to unlock in background.
	alreadyLocked = true
	// Spawning cache update in background.
	go func() {
		defer func() {
			sc.lock.Lock()
			delete(sc.keyLocks, string(key))
			close(keyLock)
			sc.lock.Unlock()
		}()

		_, err := sc.doBuild(ctx, key, value, buildFunc)
		if err != nil {
			sc.log.Warn(ctx, "failed to update stale cache value in background",
				"error", err,
				"name", sc.config.Name,
				"key", key)
		}
	}()

	return value, nil
}

func (sc *Failover) freshEnough(err error, value interface{}) (interface{}, bool) {
	var errExpired ErrExpired

	if errors.As(err, &errExpired) {
		if sc.config.MaxExpiration == 0 {
			return errExpired.Value(), true
		}

		if time.Since(errExpired.ExpiredAt()) < sc.config.MaxExpiration {
			return errExpired.Value(), true
		}
	}

	if sc.config.MaxExpiration == 0 && errors.Is(err, ErrExpiredCacheItem) && value != nil {
		return value, true
	}

	return nil, false
}

func (sc *Failover) waitForValue(ctx context.Context, key []byte, keyLock chan struct{}) (interface{}, error) {
	sc.log.Debug(ctx, "waiting for cache value", "name", sc.config.Name, "key", key)

	// Waiting for value built by keyLock owner.
	<-keyLock

	// Recurse to check and return the just-updated value.
	value, err := sc.backend.Read(ctx, key)
	if errors.Is(err, ErrExpiredCacheItem) {
		err = nil
	}

	if errors.Is(err, ErrCacheItemNotFound) {
		// Check if update failed recently.
		if err := sc.recentlyFailed(ctx, key); err != nil {
			return nil, err
		}
	}

	return value, err
}

func (sc *Failover) refreshStale(ctx context.Context, key []byte, value interface{}) error {
	sc.log.Debug(ctx, "refreshing expired value",
		"name", sc.config.Name,
		"key", key,
		"value", value)
	sc.stat.Add(ctx, MetricRefreshed, 1, "name", sc.config.Name)

	writeErr := sc.backend.Write(WithTTL(ctx, sc.config.UpdateTTL, false), key, value)
	if writeErr != nil {
		return ctxd.WrapError(ctx, writeErr, "failed to refresh expired value")
	}

	return nil
}

func (sc *Failover) doBuild(
	ctx context.Context,
	key []byte,
	value interface{},
	buildFunc func(ctx context.Context) (interface{}, error),
) (interface{}, error) {
	defer func() {
		sc.stat.Add(ctx, MetricBuild, 1, "name", sc.config.Name)
	}()
	sc.log.Debug(ctx, "building cache value", "name", sc.config.Name, "key", key)

	uVal, err := buildFunc(ctx)
	if err != nil {
		sc.stat.Add(ctx, MetricFailed, 1, "name", sc.config.Name)

		if sc.config.FailedUpdateTTL > -1 {
			writeErr := sc.Errors.Write(ctx, key, err)
			if writeErr != nil {
				sc.log.Error(ctx, "failed to cache update failure",
					"error", writeErr,
					"updateErr", err,
					"key", key,
					"name", sc.config.Name)
			}
		}

		return nil, err
	}

	writeErr := sc.backend.Write(ctx, key, uVal)
	if writeErr != nil {
		return nil, writeErr
	}

	if sc.config.ObserveMutability && value != nil {
		sc.observeMutability(ctx, uVal, value)
	}

	return uVal, err
}

func (sc *Failover) ctxSync(ctx context.Context, err error) (context.Context, bool) {
	syncUpdate := !sc.config.BackgroundUpdate || err != nil
	if syncUpdate {
		return ctx, true
	}

	// Detaching context for async update.
	return detachedContext{ctx}, false
}

func (sc *Failover) recentlyFailed(ctx context.Context, key []byte) error {
	if sc.config.FailedUpdateTTL > -1 {
		errVal, err := sc.Errors.Read(ctx, key)
		if err == nil {
			return errVal.(error)
		}
	}

	return nil
}

func (sc *Failover) observeMutability(ctx context.Context, uVal, value interface{}) {
	equal := reflect.DeepEqual(value, uVal)
	if !equal {
		sc.stat.Add(ctx, MetricChanged, 1, "name", sc.config.Name)
	}
}
