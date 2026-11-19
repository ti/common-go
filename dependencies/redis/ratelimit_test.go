package redis

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The in-process fallback used while Redis is down must enforce the same
// limit (it used to refill at "period seconds" tokens per second) and must be
// safe for concurrent use. Run with -race.
func TestRateLimitFallback(t *testing.T) {
	r := &rateLimiter{limiters: &sync.Map{}}
	var allowed atomic.Int64
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				if r.fallback("k", 10, time.Hour, 1) {
					allowed.Add(1)
				}
			}
		})
	}
	wg.Wait()
	if got := allowed.Load(); got != 10 {
		t.Fatalf("fallback allowed %d of 800 requests for a limit of 10 per hour", got)
	}
	if got := float64(fallbackRate(120, time.Minute)); got != 2 {
		t.Fatalf("fallbackRate(120, 1m) = %v, want 2 per second", got)
	}
}

// A nil or uninitialized Redis is "no shared store", not a panic.
func TestRateLimiterNilRedis(t *testing.T) {
	var r *Redis
	if r.RateLimiter() != nil {
		t.Fatal("nil Redis must return a nil store")
	}
	if (&Redis{}).RateLimiter() != nil {
		t.Fatal("uninitialized Redis must return a nil store")
	}
	if _, _, allowed := r.RateLimitN(t.Context(), "k", 1, time.Second, 1); !allowed {
		t.Fatal("RateLimitN on a nil Redis must allow")
	}
}
