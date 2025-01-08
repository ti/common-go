package ratelimit

import (
	"time"

	"github.com/jellydator/ttlcache/v3"
)

// RateLimiter the rateLimiter
type RateLimiter struct {
	limit    int
	duration time.Duration
	cache    *ttlcache.Cache[string, int]
}

// NewRateLimiter new ratelimiter
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

// Allow key
func (rl *RateLimiter) Allow(key string) bool {
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

// Reset reset key
func (rl *RateLimiter) Reset(key string) {
	rl.cache.Delete(key)
}
