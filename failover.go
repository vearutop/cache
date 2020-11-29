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

// StaleConfig is an alias of FailoverConfig.
//
// Deprecated: use FailoverConfig.
type StaleConfig = FailoverConfig

// FailoverConfig is optional configuration for NewFailover.
type FailoverConfig struct {
	// Name is added to logs and stats.
	Name string

	// Upstream is a cache instance, in-memory created by default.
	Upstream ReadWriter

	// UpstreamConfig is a configuration for in-memory cache instance if Upstream is not provided.
	UpstreamConfig MemoryConfig

	// DefaultTTL is default ttl to store cached value, only used with nil Upstream.
	//
	// Deprecated: use UpstreamConfig.TimeToLive.
	DefaultTTL time.Duration

	// FailedUpdateTTL is ttl of failed build cache, default 20s, -1 disables errors cache.
	FailedUpdateTTL time.Duration

	// UpdateTTL is a time interval to retry update, default 1 minute.
	UpdateTTL time.Duration

	// SyncUpdate enables sync update, false value is disregarded.
	//
	// Deprecated: use BackgroundUpdate to update policy.
	SyncUpdate bool

	// BackgroundUpdate enables update in background, default is sync update with updated value served.
	BackgroundUpdate bool

	// SyncRead enables upstream reading in the critical section to ensure cache miss
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

// Stale is an alias of Failover.
//
// Deprecated: use Failover.
type Stale = Failover

// Failover automatically updates cache without races.
//
// Please use NewFailover to create instance.
type Failover struct {
	// Errors caches errors of failed updates.
	Errors *Memory

	upstream ReadWriter
	lock     sync.Mutex               // Securing keyLocks
	keyLocks map[string]chan struct{} // Preventing update concurrency per key
	config   FailoverConfig
	log      ctxd.Logger
	stat     stats.Tracker
}

// NewFailover creates a Failover cache instance.
//
// Build is locked per key to avoid concurrent updates.
// Stale value is served during non-concurrent update (up to FailoverConfig.UpdateTTL long).
// Optional configuration can be provided with FailoverConfig (only first argument is used).
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

	sc.upstream = config.Upstream

	if sc.upstream == nil {
		config.UpstreamConfig.Name = config.Name
		config.UpstreamConfig.Logger = config.Logger
		config.UpstreamConfig.Stats = config.Stats
		sc.upstream = NewMemory(config.UpstreamConfig)
	}

	if config.FailedUpdateTTL > -1 {
		sc.Errors = NewMemory(MemoryConfig{
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
func (sc *Failover) Get(ctx context.Context, key string, buildFunc func(ctx context.Context) (interface{}, error)) (interface{}, error) {
	var (
		value interface{}
		err   error
	)

	// Performing initial check before critical section.
	if !sc.config.SyncRead {
		// Checking for valid value in cache store.
		value, err = sc.upstream.Read(ctx, key)
		if err == nil {
			return value, nil
		}
	}

	// Locking key for update or finding active lock.
	sc.lock.Lock()
	var keyLock chan struct{}

	alreadyLocked := false

	keyLock, alreadyLocked = sc.keyLocks[key]
	if !alreadyLocked {
		keyLock = make(chan struct{})
		sc.keyLocks[key] = keyLock
	}
	sc.lock.Unlock()

	// Releasing the lock.
	defer func() {
		if !alreadyLocked {
			sc.lock.Lock()
			delete(sc.keyLocks, key)
			close(keyLock)
			sc.lock.Unlock()
		}
	}()

	// Performing initial check in critical section.
	if sc.config.SyncRead {
		// Checking for valid value in cache store.
		value, err = sc.upstream.Read(ctx, key)
		if err == nil {
			return value, nil
		}
	}

	// If already locked waiting for completion before checking upstream again.
	if alreadyLocked {
		// Return immediately if update is in progress and stale value available.
		if val, freshEnough := sc.freshEnough(err, value); freshEnough {
			return val, nil
		}

		return sc.waitForValue(ctx, key, keyLock)
	}

	// Pushing expired value with short ttl to serve during update.
	if val, freshEnough := sc.freshEnough(err, value); freshEnough {
		err = sc.refreshStale(ctx, key, val)
		if err != nil {
			return nil, err
		}
	}

	// Check if update failed recently.
	if err := sc.recentlyFailed(ctx, key); err != nil {
		return nil, err
	}

	// Detaching context into background if SyncUpdate is disabled and there is a stale value already.
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
			delete(sc.keyLocks, key)
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
		if sc.config.MaxExpiration != 0 {
			if time.Since(errExpired.ExpiredAt()) < sc.config.MaxExpiration {
				return errExpired.Value(), true
			}
		} else {
			return errExpired.Value(), true
		}
	} else {
		if sc.config.MaxExpiration == 0 {
			if errors.Is(err, ErrExpiredCacheItem) && value != nil {
				return value, true
			}
		}
	}

	return nil, false
}

func (sc *Failover) waitForValue(ctx context.Context, key string, keyLock chan struct{}) (interface{}, error) {
	sc.log.Debug(ctx, "waiting for cache value", "name", sc.config.Name, "key", key)

	// Waiting for value built by keyLock owner.
	<-keyLock

	// Recurse to check and return the just-updated value.
	value, err := sc.upstream.Read(ctx, key)
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

func (sc *Failover) refreshStale(ctx context.Context, key string, value interface{}) error {
	sc.log.Debug(ctx, "refreshing expired value",
		"name", sc.config.Name,
		"key", key,
		"value", value)
	sc.stat.Add(ctx, MetricRefreshed, 1, "name", sc.config.Name)

	writeErr := sc.upstream.Write(WithTTL(ctx, sc.config.UpdateTTL), key, value)
	if writeErr != nil {
		return ctxd.WrapError(ctx, writeErr, "failed to refresh expired value")
	}

	return nil
}

func (sc *Failover) doBuild(
	ctx context.Context,
	key string,
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

	writeErr := sc.upstream.Write(ctx, key, uVal)
	if writeErr != nil {
		return nil, writeErr
	}

	if sc.config.ObserveMutability && value != nil {
		go sc.observeMutability(ctx, uVal, value)
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

func (sc *Failover) recentlyFailed(ctx context.Context, key string) error {
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
