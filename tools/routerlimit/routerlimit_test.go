package routerlimit

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMatch(t *testing.T) {
	routerLimit := &RouterLimit{
		Limit: []Limit{
			{
				Prefix:   "/v1/prefix/",
				Headers:  []string{"user-id", "client-id"},
				Quota:    200,
				Duration: 20 * time.Minute,
			},
		},
		Allow: []Allow{
			{
				Prefix: "/v1/prefix/",
				Match: []KV{
					{
						Key:   "client-id",
						Value: "allow",
					},
				},
				Quota:    1000,
				Duration: time.Minute,
			},
		},
		Block: []KV{
			{
				Key:   "client-id",
				Value: "block",
			},
		},
		Default: Default{
			Headers:  []string{"user-id", "client-id"},
			Quota:    100,
			Duration: 10 * time.Minute,
		},
	}
	data := routerLimit.MatchHeader("/v1/not/match", map[string][]string{
		"user-id":   {"user"},
		"client-id": {"client"},
	})
	if data.Key != ".user.client" || data.Quota != 100 {
		t.Errorf("test /v1/not/match failed expect %s but got %s quota %d", ".user.client", data.Key, data.Quota)
		return
	}
	data = routerLimit.MatchHeader("/v1/prefix/match", map[string][]string{
		"user-id":   {"user1"},
		"client-id": {"client1"},
	})

	if data.Key != ".user1.client1" || data.Quota != 200 {
		t.Errorf("test /v1/prefix/match failed expect got %s quota %d", data.Key, data.Quota)
		return
	}

	data = routerLimit.MatchHeader("/v1/prefix/block", map[string][]string{
		"client-id": {"block"},
	})

	if data.Quota != Block {
		t.Errorf("test /v1/prefix/block failed expect got %s quota %d", data.Key, data.Quota)
		return
	}

	data = routerLimit.MatchHeader("/v1/prefix/allow", map[string][]string{
		"client-id": {"allow"},
	})

	if data.Quota != 1000 {
		t.Errorf("test /v1/prefix/block failed expect got %s quota %d", data.Key, data.Quota)
		return
	}
}

// The in-memory store must count concurrent requests exactly and forget
// expired windows. Run with -race.
func TestMemLimiterConcurrent(t *testing.T) {
	m := newMemLimiter()
	var allowed atomic.Int64
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				if _, _, ok := m.AllowN(t.Context(), "k", 25, time.Minute, 1); ok {
					allowed.Add(1)
				}
			}
		})
	}
	wg.Wait()
	if got := allowed.Load(); got != 25 {
		t.Fatalf("allowed %d of 800, want 25", got)
	}
	remaining, reset, ok := m.AllowN(t.Context(), "k", 25, time.Minute, 1)
	if ok || remaining != 0 || reset <= 0 || reset > time.Minute {
		t.Fatalf("after the limit: remaining %d reset %s allowed %v", remaining, reset, ok)
	}
}

func TestMemLimiterWindowExpires(t *testing.T) {
	m := newMemLimiter()
	if _, _, ok := m.AllowN(t.Context(), "k", 1, 20*time.Millisecond, 1); !ok {
		t.Fatal("first request must pass")
	}
	if _, _, ok := m.AllowN(t.Context(), "k", 1, 20*time.Millisecond, 1); ok {
		t.Fatal("second request in the window must be limited")
	}
	time.Sleep(30 * time.Millisecond)
	if _, _, ok := m.AllowN(t.Context(), "k", 1, 20*time.Millisecond, 1); !ok {
		t.Fatal("a new window must allow again")
	}
}

func TestHTTPHandlerRetryAfter(t *testing.T) {
	limiter := &Limiter{Config: &RouterLimit{Default: Default{Headers: []string{"X-Client"}, Quota: 1, Duration: time.Minute}}}
	h := NewHandler(limiter)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	call := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/things", nil)
		req.Header.Set("X-Client", "c1")
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := call(); rec.Code != http.StatusNoContent {
		t.Fatalf("first: %d", rec.Code)
	}
	rec := call()
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "60" {
		t.Fatalf("second: %d Retry-After %q body %s", rec.Code, rec.Header().Get("Retry-After"), rec.Body)
	}
}
