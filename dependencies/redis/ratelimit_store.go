package redis

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/redis/rueidis"

	"github.com/ti/common-go/tools/ratelimit"
)

// gcraScript is the token bucket of ratelimit.GCRA, run atomically in Redis.
// The script is ratelimit.RedisScript (tools/ratelimit/gcra.lua), the one
// file every backend loads; its header documents key, arguments and results.
var gcraScript = rueidis.NewLuaScript(ratelimit.RedisScript)

// rateLimitTimeout bounds one bucket operation. A limiter that waits longer
// than this for Redis costs more than the requests it protects against; the
// caller (ratelimit.Limiter) falls back to in-memory buckets on error.
const rateLimitTimeout = 200 * time.Millisecond

// RateLimiter returns a store that keeps the token buckets of a
// ratelimit.Limiter in this Redis, so that limits hold across replicas:
//
//	gs := grpcmux.NewServer(
//	    grpcmux.WithConfig(&cfg.Apis),                           // apis.rateLimit
//	    grpcmux.WithRateLimitStore(cfg.Dependencies.Redis.RateLimiter()),
//	)
//
// Each check is one EVALSHA round trip (a GET and a SET on a single key), so
// it works on Redis Cluster. On a nil or uninitialized *Redis it returns nil,
// which the limiter reads as "keep the buckets in memory": an optional Redis
// dependency needs no special case.
func (r *Redis) RateLimiter() ratelimit.Store {
	if r == nil || r.client == nil {
		return nil
	}
	return rateLimitStore{client: r.client}
}

type rateLimitStore struct {
	client rueidis.Client
}

// Take implements ratelimit.Store.
func (s rateLimitStore) Take(ctx context.Context, key string, limit ratelimit.Limit, n int) (ratelimit.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, rateLimitTimeout)
	defer cancel()
	vals, err := gcraScript.Exec(ctx, s.client, []string{key}, []string{
		strconv.FormatInt(limit.Interval().Microseconds(), 10),
		strconv.Itoa(limit.Burst),
		strconv.Itoa(n),
	}).AsIntSlice()
	if err != nil {
		return ratelimit.Result{}, err
	}
	if len(vals) != 4 {
		return ratelimit.Result{}, errors.New("redis: rate limit script returned " + strconv.Itoa(len(vals)) + " values, want 4")
	}
	return ratelimit.Result{
		Allowed:    vals[0] == 1,
		Remaining:  int(vals[1]),
		RetryAfter: time.Duration(vals[2]) * time.Microsecond,
		ResetAfter: time.Duration(vals[3]) * time.Microsecond,
	}, nil
}
