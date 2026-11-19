package ratelimit

import (
	"context"
	_ "embed" // gcra.lua
	"fmt"
	"hash/maphash"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RedisScript is the Lua script that applies [GCRA] to one Redis key
// atomically (the file gcra.lua next to this one). Services in other
// languages that share a Redis with a Go service load the same file; its
// header documents the key format, the arguments and the four results.
// (*redis.Redis).RateLimiter runs it.
//
//go:embed gcra.lua
var RedisScript string

// Limit is a token bucket: Rate tokens are added every Period, and at most
// Burst tokens can be saved up. A caller that has been idle may therefore
// send Burst requests at once and is then held to Rate per Period.
type Limit struct {
	Rate   int
	Period time.Duration
	Burst  int
}

// ParseLimit parses a rate such as "10/s", "600/m", "1000/h" or "5/10s"
// (five every ten seconds). A burst of 0 means "the rate's count": "100/m"
// then allows 100 at once and refills one token every 600ms.
func ParseLimit(rate string, burst int) (Limit, error) {
	count, per, ok := strings.Cut(strings.TrimSpace(rate), "/")
	if !ok {
		return Limit{}, fmt.Errorf("rate %q: want <count>/<period>, for example 10/s", rate)
	}
	n, err := strconv.Atoi(strings.TrimSpace(count))
	if err != nil || n <= 0 {
		return Limit{}, fmt.Errorf("rate %q: count must be a positive integer", rate)
	}
	per = strings.TrimSpace(per)
	var period time.Duration
	switch per {
	case "s", "sec", "second":
		period = time.Second
	case "m", "min", "minute":
		period = time.Minute
	case "h", "hour":
		period = time.Hour
	case "d", "day":
		period = 24 * time.Hour
	default:
		period, err = time.ParseDuration(per)
		if err != nil || period <= 0 {
			return Limit{}, fmt.Errorf("rate %q: period must be s, m, h, d or a duration such as 10s", rate)
		}
	}
	if burst < 0 {
		return Limit{}, fmt.Errorf("burst %d must not be negative", burst)
	}
	if burst == 0 {
		burst = n
	}
	l := Limit{Rate: n, Period: period, Burst: burst}
	// The bucket arithmetic is integer (nanoseconds in memory, microseconds
	// in Redis): refuse limits it cannot represent rather than misbehave.
	if period/time.Duration(n) < time.Microsecond {
		return Limit{}, fmt.Errorf("rate %q is more than one request per microsecond", rate)
	}
	if time.Duration(burst) > maxTolerance/l.Interval() {
		return Limit{}, fmt.Errorf("rate %q with burst %d: a full bucket would take longer than %s to refill", rate, burst, maxTolerance)
	}
	return l, nil
}

// maxTolerance bounds interval x burst, the time an empty bucket needs to
// refill completely. Ten years is far beyond any real limit and keeps every
// product in the arithmetic well inside int64 nanoseconds and inside the 2^53
// microseconds the Redis script (Lua doubles) computes exactly.
const maxTolerance = 10 * 365 * 24 * time.Hour

// String formats the limit the way ParseLimit reads it.
func (l Limit) String() string {
	return strconv.Itoa(l.Rate) + "/" + l.Period.String() + " burst " + strconv.Itoa(l.Burst)
}

// Interval is the time one token takes to refill.
func (l Limit) Interval() time.Duration {
	if l.Rate <= 0 {
		return l.Period
	}
	return max(l.Period/time.Duration(l.Rate), time.Microsecond)
}

// Result is the outcome of taking tokens from a bucket.
type Result struct {
	// Allowed reports whether the tokens were taken.
	Allowed bool
	// Remaining is the number of tokens left in the bucket.
	Remaining int
	// RetryAfter is how long to wait until the request would be allowed.
	// Zero when Allowed.
	RetryAfter time.Duration
	// ResetAfter is how long until the bucket is full again.
	ResetAfter time.Duration
}

// Store keeps the buckets. Take removes n tokens from the bucket named key,
// creating it full if it does not exist. A key must always be used with the
// same limit: what a bucket stores is only meaningful for the interval and
// burst it was written under. [Limiter] guarantees that by making the limit
// part of the key. Implementations must be safe for
// concurrent use; a shared implementation (Redis) makes the limit hold across
// replicas.
type Store interface {
	Take(ctx context.Context, key string, limit Limit, n int) (Result, error)
}

// GCRA applies the generic cell rate algorithm, the form of a token bucket
// that needs one number per key: tat, the "theoretical arrival time" at which
// the bucket is full again. All values are in the same unit (the stores use
// nanoseconds and microseconds since the Unix epoch). It returns the tat to
// store. For a denied request that is the value that was passed in (nothing
// is consumed), except after a backward clock step, see below; a store writes
// the result whenever it differs from what it holds.
//
// A stored tat can never be further ahead than now + interval*burst (an
// empty bucket). When it is, the clock stepped backwards since it was written
// (a wall-clock adjustment, a Redis failover to a server with another clock);
// the bucket is then treated as empty, and stored as such, instead of locking
// the key out for the size of the step. That reading is only right when the
// value was written under the same interval and burst, which is why a key
// must never be shared by two limits (see [Store]).
//
// It is exported so that stores implemented in Go compute exactly what the
// Redis script computes.
func GCRA(tat, now, interval int64, burst, n int) (newTAT int64, allowed bool, remaining, retryAfter, resetAfter int64) {
	tolerance := interval * int64(burst)
	if tat < now {
		tat = now
	}
	if tat > now+tolerance {
		tat = now + tolerance
	}
	next := tat + interval*int64(n)
	if next-now > tolerance {
		return tat, false, (tolerance - (tat - now)) / interval, next - tolerance - now, tat - now
	}
	return next, true, (tolerance - (next - now)) / interval, 0, next - now
}

const memoryShards = 64

// MemoryStore keeps buckets in process memory. Limits hold per process: with
// N replicas behind a load balancer a caller gets up to N times the limit.
// It needs no background goroutine; idle buckets are dropped lazily.
type MemoryStore struct {
	seed   maphash.Seed
	now    func() time.Time
	shards [memoryShards]memoryShard
}

type memoryShard struct {
	mu        sync.Mutex
	tat       map[string]int64 // UnixNano at which the bucket is full again
	lastSweep int64
}

// NewMemoryStore returns an empty in-memory store. It measures time with the
// monotonic clock, so adjustments of the wall clock do not affect buckets.
func NewMemoryStore() *MemoryStore {
	start := time.Now()
	// start.Add(time.Since(start)) advances with the monotonic clock only.
	return newMemoryStore(func() time.Time { return start.Add(time.Since(start)) })
}

func newMemoryStore(now func() time.Time) *MemoryStore {
	m := &MemoryStore{seed: maphash.MakeSeed(), now: now}
	for i := range m.shards {
		m.shards[i].tat = make(map[string]int64)
	}
	return m
}

// sweepInterval is how often a shard drops the buckets that are full again
// (which is the same state as a bucket that was never used).
const sweepInterval = int64(time.Minute)

// Take implements Store.
func (m *MemoryStore) Take(_ context.Context, key string, limit Limit, n int) (Result, error) {
	now := m.now().UnixNano()
	sh := &m.shards[maphash.String(m.seed, key)%memoryShards]
	sh.mu.Lock()
	if now-sh.lastSweep > sweepInterval {
		for k, tat := range sh.tat {
			if tat <= now {
				delete(sh.tat, k)
			}
		}
		sh.lastSweep = now
	}
	stored, exists := sh.tat[key]
	tat, allowed, remaining, retry, reset := GCRA(stored, now, int64(limit.Interval()), limit.Burst, n)
	if allowed || (exists && tat != stored) {
		sh.tat[key] = tat
	}
	sh.mu.Unlock()
	return Result{
		Allowed:    allowed,
		Remaining:  int(remaining),
		RetryAfter: time.Duration(retry),
		ResetAfter: time.Duration(reset),
	}, nil
}

// Len returns the number of buckets currently held (for tests and debugging).
func (m *MemoryStore) Len() int {
	var n int
	for i := range m.shards {
		m.shards[i].mu.Lock()
		n += len(m.shards[i].tat)
		m.shards[i].mu.Unlock()
	}
	return n
}
