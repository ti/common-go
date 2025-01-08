// Package redisrate refer: https://github.com/go-redis/redis_rate/tree/v7
package redisrate

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/rueidis"
	"golang.org/x/time/rate"
)

const redisPrefix = "rate"

// Limiter controls how frequently events are allowed to happen.
type Limiter struct {
	redisCli rueidis.Client
	// Optional fallback limiter used when Redis is unavailable.
	Fallback *rate.Limiter
}

// NewLimiter new redis rate limiter.
func NewLimiter(redisCli rueidis.Client) *Limiter {
	return &Limiter{
		redisCli: redisCli,
	}
}

// Reset resets the rate limit for the name in the given rate limit period.
func (l *Limiter) Reset(ctx context.Context, name string, period time.Duration) error {
	secs := int64(period / time.Second)
	slot := time.Now().Unix() / secs
	name = allowName(name, slot)
	return l.redisCli.Do(ctx, l.redisCli.B().Del().Key(name).Build()).Error()
}

// ResetRate resets the rate limit for the name and limit.
func (l *Limiter) ResetRate(ctx context.Context, name string, rateLimit rate.Limit) error {
	if rateLimit == 0 {
		return nil
	}
	if rateLimit == rate.Inf {
		return nil
	}

	_, period := limitPeriod(rateLimit)
	slot := time.Now().UnixNano() / period.Nanoseconds()

	name = allowRateName(name, period, slot)
	return l.redisCli.Do(ctx, l.redisCli.B().Del().Key(name).Build()).Error()
}

// AllowN reports whether an event with given name may happen at time now.
// It allows up to maxn events within period, with each interaction
// incrementing the limit by n.
func (l *Limiter) AllowN(ctx context.Context,
	name string, maxn int, period time.Duration, n int,
) (remaining int, delay time.Duration, allow bool, err error) {
	secs := int64(period / time.Second)
	utime := time.Now().Unix()
	slot := utime / secs
	delayInt := ((slot+1)*secs - utime) * int64(time.Second)
	delay = time.Duration(delayInt)
	if l.Fallback != nil {
		allow = l.Fallback.Allow()
	}
	name = allowName(name, slot)

	var count int
	count, err = l.incr(ctx, name, secs, n)
	if err != nil {
		return
	}
	allow = count <= maxn
	remaining = maxn - count
	if remaining < 0 {
		remaining = 0
	}
	return
}

// AllowRate reports whether an event may happen at time now.
// It allows up to rateLimit events each second.
func (l *Limiter) AllowRate(ctx context.Context, name string, rateLimit rate.Limit) (delay time.Duration, allow bool) {
	if rateLimit == 0 {
		return 0, false
	}
	if rateLimit == rate.Inf {
		return 0, true
	}

	limit, period := limitPeriod(rateLimit)
	now := time.Now()
	slot := now.UnixNano() / period.Nanoseconds()
	name = allowRateName(name, period, slot)
	periodSecs := int64(period / time.Second)
	count, err := l.incr(ctx, name, periodSecs, 1)
	if err == nil {
		allow = count <= limit
	} else if l.Fallback != nil {
		allow = l.Fallback.Allow()
	}

	if !allow {
		delay = time.Duration(slot+1)*period - time.Duration(now.UnixNano())
	}

	return delay, allow
}

func limitPeriod(rl rate.Limit) (limit int, period time.Duration) {
	period = time.Second
	if rl < 1 {
		limit = 1
		period *= time.Duration(1 / rl)
	} else {
		limit = int(rl)
	}
	return limit, period
}

func (l *Limiter) incr(ctx context.Context, name string, periodSecond int64, n int) (rateCount int, err error) {
	resps := l.redisCli.DoMulti(ctx,
		l.redisCli.B().Incrby().Key(name).Increment(int64(n)).Build(),
		l.redisCli.B().Expire().Key(name).Seconds(periodSecond+30).Build(),
	)
	for i, v := range resps {
		err = v.Error()
		if err != nil {
			return 0, err
		}
		if i == 0 {
			var countResult int64
			countResult, err = v.AsInt64()
			if err != nil {
				return
			}
			rateCount = int(countResult)
		}
	}
	return
}

func allowName(name string, slot int64) string {
	return fmt.Sprintf("%s:%s-%d", redisPrefix, name, slot)
}

func allowRateName(name string, period time.Duration, slot int64) string {
	return fmt.Sprintf("%s:%s-%d-%d", redisPrefix, name, period, slot)
}
