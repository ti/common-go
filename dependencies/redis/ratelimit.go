package redis

import (
	"context"
	"encoding/base64"
	"sync"
	"time"

	"github.com/redis/rueidis"
	"github.com/ti/common-go/dependencies/redis/redisrate"
	"github.com/ti/common-go/log"
	"golang.org/x/time/rate"
)

// RateLimitN Rate limit by redis
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
	r := &rateLimiter{
		redis:    redisrate.NewLimiter(redisClient),
		limiters: &sync.Map{},
	}
	go func() {
		ticker := time.NewTicker(checkInterval)
		for {
			<-ticker.C
			r.limiters.Range(func(key, value any) bool {
				v := value.(*timerRate)
				if time.Since(v.lastSeen) > cleanupDuration {
					r.limiters.Delete(key)
				}
				return true
			})
		}
	}()
	return r
}

type rateLimiter struct {
	redis    *redisrate.Limiter
	limiters *sync.Map
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

func (r *rateLimiter) fallback(key string, limit int, interval time.Duration, n int) bool {
	limiter := &timerRate{
		limiter:  rate.NewLimiter(rate.Limit(interval/time.Second)/rate.Limit(time.Second.Seconds()), limit),
		lastSeen: time.Now(),
	}
	v, loaded := r.limiters.LoadOrStore(key, limiter)
	if loaded {
		limiter = v.(*timerRate)
		limiter.lastSeen = time.Now()
	}
	return limiter.limiter.AllowN(limiter.lastSeen, n)
}

type timerRate struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}
