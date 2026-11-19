package ratelimit

import (
	"sync"
	"time"

	"github.com/jellydator/ttlcache/v3"
)

// RateLimiter is a standalone fixed-window counter: at most limit events per
// key in each window of the given duration, counted in process memory. For
// request rate limiting on a grpcmux server use [Limiter] (the rateLimit
// configuration) instead.
type RateLimiter struct {
	limit    int
	duration time.Duration
	cache    *ttlcache.Cache[string, int]
	mu       sync.Mutex // makes the read-increment-write in Allow atomic
}

// NewRateLimiter returns a RateLimiter that keeps at most capacity keys. It
// starts a goroutine that evicts expired keys; call Stop when the limiter is
// no longer needed.
func NewRateLimiter(duration time.Duration, limit int, capacity uint64) *RateLimiter {
	cache := ttlcache.New[string, int](
		ttlcache.WithTTL[string, int](duration),
		ttlcache.WithDisableTouchOnHit[string, int](),
		ttlcache.WithCapacity[string, int](capacity))
	go cache.Start()
	return &RateLimiter{
		limit:    limit,
		duration: duration,
		cache:    cache,
	}
}

// Stop ends the eviction goroutine. The limiter keeps working, expired keys
// are then only dropped when the capacity is reached.
func (rl *RateLimiter) Stop() {
	rl.cache.Stop()
}

// Allow counts one event for key and reports whether it is within the limit.
func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	item, ok := rl.cache.GetOrSet(key, 1)
	if !ok {
		return true
	}
	if item.Value() < rl.limit {
		if dl := time.Until(item.ExpiresAt()); dl > 0 {
			rl.cache.Set(key, item.Value()+1, dl)
		}
		return true
	}
	return false
}

// Reset forgets the count of key.
func (rl *RateLimiter) Reset(key string) {
	rl.cache.Delete(key)
}
