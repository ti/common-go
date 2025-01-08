package routerlimit

import (
	"context"
	"fmt"
	"time"

	"github.com/jellydator/ttlcache/v3"
)

// Limiter the limiter struct for hold fn and config
type Limiter struct {
	PersistenceFn PersistenceFn
	Config        *RouterLimit
}

// PersistenceFn the limit persistence fn for store limit status
type PersistenceFn func(ctx context.Context, key string,
	limit int, period time.Duration, n int) (remaining int, reset time.Duration, allowed bool)

type memLimiter struct {
	cache *ttlcache.Cache[string, int]
}

func newMemLimiter() *memLimiter {
	opts := []ttlcache.Option[string, int]{
		ttlcache.WithDisableTouchOnHit[string, int](),
		ttlcache.WithCapacity[string, int](1024 * 1024 * 500),
	}
	return &memLimiter{
		cache: ttlcache.New[string, int](opts...),
	}
}

// AllowN ratelimit allow n times
func (m *memLimiter) AllowN(_ context.Context, key string,
	limit int, period time.Duration, n int,
) (remaining int, reset time.Duration, allowed bool) {
	memKey := fmt.Sprintf("%s.%d", key, period.Microseconds()/1000)
	data := m.cache.Get(memKey)
	if data == nil {
		remaining = limit - n
		reset = period
		allowed = true
		m.cache.Set(memKey, n, period)
		return
	}
	reset = time.Until(data.ExpiresAt())
	count := data.Value()
	remaining = limit - count - n
	allowed = remaining >= 0
	if !allowed {
		remaining = 0
	}
	if allowed && reset > time.Microsecond {
		m.cache.Set(memKey, count+n, reset)
	}
	return
}
