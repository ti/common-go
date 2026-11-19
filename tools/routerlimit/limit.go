package routerlimit

import (
	"context"
	"strconv"
	"sync"
	"time"
)

// Limiter the limiter struct for hold fn and config
type Limiter struct {
	PersistenceFn PersistenceFn
	Config        *RouterLimit
}

// PersistenceFn the limit persistence fn for store limit status
type PersistenceFn func(ctx context.Context, key string,
	limit int, period time.Duration, n int) (remaining int, reset time.Duration, allowed bool)

// memLimiter is the default PersistenceFn: fixed windows counted in process
// memory. A window starts with the first request for a key and ends period
// later.
type memLimiter struct {
	mu        sync.Mutex
	windows   map[string]*memWindow
	lastSweep time.Time
}

type memWindow struct {
	count   int
	expires time.Time
}

func newMemLimiter() *memLimiter {
	return &memLimiter{windows: make(map[string]*memWindow)}
}

// memSweepInterval is how often expired windows are dropped, so that keys
// seen once (client IPs) do not accumulate.
const memSweepInterval = time.Minute

// AllowN ratelimit allow n times
func (m *memLimiter) AllowN(_ context.Context, key string,
	limit int, period time.Duration, n int,
) (remaining int, reset time.Duration, allowed bool) {
	memKey := key + "." + strconv.FormatInt(period.Milliseconds(), 10)
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	if now.Sub(m.lastSweep) > memSweepInterval {
		for k, w := range m.windows {
			if !now.Before(w.expires) {
				delete(m.windows, k)
			}
		}
		m.lastSweep = now
	}
	w := m.windows[memKey]
	if w == nil || !now.Before(w.expires) {
		w = &memWindow{expires: now.Add(period)}
		m.windows[memKey] = w
	}
	reset = w.expires.Sub(now)
	if w.count+n > limit {
		return 0, reset, false
	}
	w.count += n
	return limit - w.count, reset, true
}
