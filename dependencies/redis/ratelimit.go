package redis

import (
	"context"
	"encoding/base64"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/rueidis"
	"golang.org/x/time/rate"

	"github.com/ti/common-go/dependencies/redis/redisrate"
	"github.com/ti/common-go/log"
)

// RateLimitN counts n events for key in a fixed window of the given period and
// reports whether the count is within limit. It has the signature of
// routerlimit.PersistenceFn. When Redis fails (after one retry) it falls back
// to a per-process token bucket of the same limit. A nil Redis allows
// everything.
//
// For request rate limiting from configuration use [Redis.RateLimiter]
// (token buckets, grpcmux.WithRateLimitStore) instead.
func (r *Redis) RateLimitN(ctx context.Context, key string, limit int,
	period time.Duration, n int,
) (remaining int, reset time.Duration, allowed bool) {
	// allow redis be nil
	if r == nil || r.client == nil {
		allowed = true
		return
	}
	return r.rateLimiter.rateLimitN(ctx, key, limit, period, n)
}

func newRateLimiter(redisClient rueidis.Client,
	checkInterval, cleanupDuration time.Duration,
) *rateLimiter {
	done := make(chan struct{})
	limiters := &sync.Map{}
	r := &rateLimiter{
		redis:    redisrate.NewLimiter(redisClient),
		limiters: limiters,
		done:     done,
	}
	ticker := time.NewTicker(checkInterval)
	go func() {
		for {
			select {
			case <-ticker.C:
				limiters.Range(func(key, value any) bool {
					v := value.(*timerRate)
					if time.Since(time.Unix(0, v.lastSeen.Load())) > cleanupDuration {
						limiters.Delete(key)
					}
					return true
				})
			case <-done:
				ticker.Stop()
				return
			}
		}
	}()
	r.cleanup = runtime.AddCleanup(r, func(ch chan struct{}) {
		close(ch)
	}, done)
	return r
}

type rateLimiter struct {
	redis    *redisrate.Limiter
	limiters *sync.Map
	done     chan struct{}
	cleanup  runtime.Cleanup
}

func (r *rateLimiter) rateLimitN(ctx context.Context, key string, limit int,
	period time.Duration, n int,
) (remaining int, reset time.Duration, allowed bool) {
	var err error
	// Prevent special strings from appearing in key
	key = base64.RawURLEncoding.EncodeToString([]byte(key))
	firstCtx, cc := context.WithTimeout(ctx, 300*time.Millisecond)
	remaining, reset, allowed, err = r.redis.AllowN(firstCtx, key, limit, period, n)
	cc()
	if err != nil {
		secondCtx, cc := context.WithTimeout(ctx, 300*time.Millisecond)
		remaining, reset, allowed, err = r.redis.AllowN(secondCtx, key, limit, period, n)
		cc()
	}
	if err != nil {
		log.Extract(ctx).Action("redis.RateLimit").Warn(err.Error())
		allowed = r.fallback(key, limit, period, n)
		remaining = limit
		reset = period
		return
	}
	return
}

// fallback limits in process memory while Redis is unreachable: a token bucket
// that refills limit tokens per interval and holds at most limit.
func (r *rateLimiter) fallback(key string, limit int, interval time.Duration, n int) bool {
	now := time.Now()
	limiter := &timerRate{limiter: rate.NewLimiter(fallbackRate(limit, interval), limit)}
	limiter.lastSeen.Store(now.UnixNano())
	if v, loaded := r.limiters.LoadOrStore(key, limiter); loaded {
		limiter = v.(*timerRate)
		limiter.lastSeen.Store(now.UnixNano())
	}
	return limiter.limiter.AllowN(now, n)
}

// fallbackRate is limit events per interval, as events per second.
func fallbackRate(limit int, interval time.Duration) rate.Limit {
	if interval <= 0 {
		return rate.Inf
	}
	return rate.Limit(float64(limit) / interval.Seconds())
}

type timerRate struct {
	limiter  *rate.Limiter
	lastSeen atomic.Int64 // UnixNano; written by requests, read by the cleanup goroutine
}
