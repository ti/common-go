package ratelimit

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// counterValue reads a counter from the default registry: the sum over the
// series of family name whose labels include all of want.
func counterValue(t *testing.T, name string, want map[string]string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var sum float64
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
	series:
		for _, m := range f.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			for k, v := range want {
				if labels[k] != v {
					continue series
				}
			}
			sum += m.GetCounter().GetValue()
		}
	}
	return sum
}

func TestParseLimit(t *testing.T) {
	tests := []struct {
		rate  string
		burst int
		want  Limit
	}{
		{"10/s", 0, Limit{10, time.Second, 10}},
		{"600/m", 30, Limit{600, time.Minute, 30}},
		{" 1000 / h ", 0, Limit{1000, time.Hour, 1000}},
		{"5/10s", 2, Limit{5, 10 * time.Second, 2}},
		{"1/d", 0, Limit{1, 24 * time.Hour, 1}},
	}
	for _, tt := range tests {
		got, err := ParseLimit(tt.rate, tt.burst)
		if err != nil || got != tt.want {
			t.Errorf("ParseLimit(%q, %d) = %v, %v; want %v", tt.rate, tt.burst, got, err, tt.want)
		}
	}
	for _, bad := range []string{"", "10", "0/s", "-1/s", "ten/s", "10/fortnight", "10/-1s"} {
		if _, err := ParseLimit(bad, 0); err == nil {
			t.Errorf("ParseLimit(%q): expected an error", bad)
		}
	}
	if _, err := ParseLimit("1/s", -1); err == nil {
		t.Error("negative burst: expected an error")
	}
}

// fakeClock is a manually advanced clock.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func TestMemoryStoreTokenBucket(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	m := newMemoryStore(clock.Now)
	limit := Limit{Rate: 2, Period: time.Second, Burst: 4} // one token per 500ms, four saved up
	ctx := context.Background()

	for i := range 4 {
		res, _ := m.Take(ctx, "k", limit, 1)
		if !res.Allowed || res.Remaining != 3-i {
			t.Fatalf("burst request %d: %+v", i, res)
		}
	}
	res, _ := m.Take(ctx, "k", limit, 1)
	if res.Allowed || res.RetryAfter != 500*time.Millisecond || res.Remaining != 0 {
		t.Fatalf("fifth request must wait one interval: %+v", res)
	}
	// A denied request does not push the bucket further back.
	res, _ = m.Take(ctx, "k", limit, 1)
	if res.Allowed || res.RetryAfter != 500*time.Millisecond {
		t.Fatalf("repeated denial changed the wait: %+v", res)
	}
	clock.Advance(499 * time.Millisecond)
	if res, _ = m.Take(ctx, "k", limit, 1); res.Allowed {
		t.Fatalf("allowed 1ms early: %+v", res)
	}
	clock.Advance(time.Millisecond)
	if res, _ = m.Take(ctx, "k", limit, 1); !res.Allowed {
		t.Fatalf("not allowed after the interval: %+v", res)
	}
	// Other keys have their own bucket.
	if res, _ = m.Take(ctx, "other", limit, 1); !res.Allowed || res.Remaining != 3 {
		t.Fatalf("other key: %+v", res)
	}
	// After the bucket refilled completely the full burst is available again.
	clock.Advance(time.Minute)
	if res, _ = m.Take(ctx, "k", limit, 1); !res.Allowed || res.Remaining != 3 {
		t.Fatalf("after refill: %+v", res)
	}
}

func TestMemoryStoreDropsIdleBuckets(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	m := newMemoryStore(clock.Now)
	limit := Limit{Rate: 1, Period: time.Second, Burst: 1}
	for i := range 1000 {
		_, _ = m.Take(context.Background(), "ip-"+strconv.Itoa(i), limit, 1)
	}
	if m.Len() != 1000 {
		t.Fatalf("Len = %d, want 1000", m.Len())
	}
	clock.Advance(2 * time.Minute)
	// Each shard sweeps when it is next used; 2000 new keys reach all 64.
	for i := range 2000 {
		_, _ = m.Take(context.Background(), "new-"+strconv.Itoa(i), limit, 1)
	}
	if n := m.Len(); n != 2000 {
		t.Fatalf("idle buckets were not dropped: %d buckets, want 2000", n)
	}
}

// Concurrent callers on one key never get more than burst plus what refills
// while the test runs. Run with -race.
func TestMemoryStoreConcurrent(t *testing.T) {
	m := NewMemoryStore()
	limit := Limit{Rate: 1, Period: time.Hour, Burst: 50}
	var allowed atomic.Int64
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 200 {
				if res, _ := m.Take(context.Background(), "k", limit, 1); res.Allowed {
					allowed.Add(1)
				}
			}
		})
	}
	wg.Wait()
	if got := allowed.Load(); got != 50 {
		t.Fatalf("allowed %d of 3200 concurrent requests, want exactly 50", got)
	}
}

func newTestLimiter(t *testing.T, cfg Config, opts ...Option) *Limiter {
	t.Helper()
	l, err := New(cfg, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestLimiterRuleSelection(t *testing.T) {
	l := newTestLimiter(t, Config{
		Default: &Rule{Rate: "1/h", Burst: 3},
		Rules: []Rule{
			{Match: "/auth.v1.AuthService/*", Rate: "1/h", Burst: 2, Key: KeyIP},
			{Match: "/note.v1.NoteService/*", Rate: "1/h", Burst: 5},
			{Match: "/note.v1.NoteService/WatchNotes", Rate: "1/h", Burst: 1},
			{Match: "/internal.v1.SyncService/*", Unlimited: true},
		},
	})
	tests := []struct {
		stage  Stage
		method string
		rule   string // "" = not limited
	}{
		{PostAuth, "/other.v1.OtherService/Do", "default"},
		{PreAuth, "/other.v1.OtherService/Do", ""}, // the default is keyed by subject
		{PreAuth, "/auth.v1.AuthService/Token", "/auth.v1.AuthService/*"},
		{PostAuth, "/auth.v1.AuthService/Token", "default"},
		{PostAuth, "/note.v1.NoteService/GetNote", "/note.v1.NoteService/*"},
		{PostAuth, "/note.v1.NoteService/WatchNotes", "/note.v1.NoteService/WatchNotes"},
		{PostAuth, "/note.v1.NoteServiceAdmin/Purge", "default"}, // not the same service
		{PostAuth, "/internal.v1.SyncService/Push", ""},
		{PreAuth, "/internal.v1.SyncService/Push", ""},
		{PostAuth, "/grpc.health.v1.Health/Check", ""},
		{PostAuth, "/healthz", ""},
		{PostAuth, "/readyz", ""},
	}
	for _, tt := range tests {
		d := l.Allow(context.Background(), tt.stage, tt.method, "198.51.100.9", "alice")
		if d.Rule != tt.rule {
			t.Errorf("stage %d %s: rule %q, want %q", tt.stage, tt.method, d.Rule, tt.rule)
		}
	}
}

func TestLimiterKeys(t *testing.T) {
	l := newTestLimiter(t, Config{
		Default: &Rule{Rate: "1/h", Burst: 2},
		Rules: []Rule{
			{Match: "/a.v1.A/*", Rate: "1/h", Burst: 1, PerMethod: true},
			{Match: "/login", Rate: "1/h", Burst: 1, Key: KeyIP},
		},
	})
	ctx := context.Background()
	take := func(stage Stage, method, ip, subject string) bool {
		return !l.Allow(ctx, stage, method, ip, subject).Limited
	}

	// The default bucket is shared by all of a subject's methods ...
	if !take(PostAuth, "/x.v1.X/One", "1.1.1.1", "alice") || !take(PostAuth, "/x.v1.X/Two", "2.2.2.2", "alice") {
		t.Fatal("first two requests of alice must pass")
	}
	if take(PostAuth, "/x.v1.X/Three", "3.3.3.3", "alice") {
		t.Fatal("alice's third request must be limited whatever her IP is")
	}
	// ... and is separate per subject.
	if !take(PostAuth, "/x.v1.X/One", "1.1.1.1", "bob") {
		t.Fatal("bob has his own bucket")
	}
	// Anonymous callers fall back to the client IP.
	for i, want := range []bool{true, true, false} {
		if got := take(PostAuth, "/x.v1.X/One", "9.9.9.9", ""); got != want {
			t.Fatalf("anonymous request %d from one IP: allowed %v, want %v", i+1, got, want)
		}
	}
	if !take(PostAuth, "/x.v1.X/One", "9.9.9.8", "") {
		t.Fatal("another IP has its own bucket")
	}
	// perMethod: one bucket per method.
	if !take(PostAuth, "/a.v1.A/One", "", "carol") || !take(PostAuth, "/a.v1.A/Two", "", "carol") ||
		take(PostAuth, "/a.v1.A/One", "", "carol") {
		t.Fatal("perMethod buckets")
	}
	// key: ip ignores the subject and is checked before authentication.
	if !take(PreAuth, "/login", "5.5.5.5", "") || take(PreAuth, "/login", "5.5.5.5", "") {
		t.Fatal("ip rule")
	}
	// No identity at all (an in-process call): not limited.
	for range 5 {
		if !take(PostAuth, "/x.v1.X/One", "", "") {
			t.Fatal("a request without IP and subject must not be limited")
		}
	}
}

func TestLimiterDecision(t *testing.T) {
	l := newTestLimiter(t, Config{Default: &Rule{Rate: "1/m", Burst: 1}})
	ctx := context.Background()
	if d := l.Allow(ctx, PostAuth, "/x.v1.X/One", "", "alice"); d.Limited || d.Rule != "default" || d.Key != KeySubject {
		t.Fatalf("first: %+v", d)
	}
	d := l.Allow(ctx, PostAuth, "/x.v1.X/One", "", "alice")
	if !d.Limited || d.RetryAfter <= 59*time.Second || d.RetryAfter > time.Minute {
		t.Fatalf("second: %+v", d)
	}
}

func TestLimiterOff(t *testing.T) {
	for _, cfg := range []Config{{}, {Disabled: true, Default: &Rule{Rate: "1/s"}}} {
		l, err := New(cfg)
		if err != nil || l != nil {
			t.Fatalf("New(%+v) = %v, %v; want nil, nil", cfg, l, err)
		}
		if d := l.Allow(context.Background(), PostAuth, "/x", "1.1.1.1", "a"); d.Limited {
			t.Fatal("a nil Limiter must allow")
		}
	}
}

func TestLimiterConfigErrors(t *testing.T) {
	bad := []Config{
		{Default: &Rule{}},
		{Default: &Rule{Rate: "1/s", Match: "/x"}},
		{Default: &Rule{Rate: "1/s", Key: "tenant"}},
		{Rules: []Rule{{Rate: "1/s"}}},
		{Rules: []Rule{{Match: "/pkg.Service*", Rate: "1/s"}}},
		{Rules: []Rule{{Match: "/x", Rate: "fast"}}},
		{Default: &Rule{Rate: "1/s"}, TrustedProxies: []string{"nonsense"}},
	}
	for _, cfg := range bad {
		if _, err := New(cfg); err == nil {
			t.Errorf("New(%+v): expected an error", cfg)
		}
	}
}

// failingStore fails every call.
type failingStore struct{ calls atomic.Int64 }

func (f *failingStore) Take(context.Context, string, Limit, int) (Result, error) {
	f.calls.Add(1)
	return Result{}, errors.New("redis: connection refused")
}

// A failing shared store degrades to in-memory limits; it does not lift them.
func TestLimiterStoreFailureFallsBackToMemory(t *testing.T) {
	store := &failingStore{}
	l := newTestLimiter(t, Config{Default: &Rule{Rate: "1/h", Burst: 2}}, WithStore(store))
	before := counterValue(t, "ratelimit_store_errors_total", nil)
	ctx := context.Background()
	var limited int
	for range 5 {
		if l.Allow(ctx, PostAuth, "/x", "", "alice").Limited {
			limited++
		}
	}
	if limited != 3 {
		t.Fatalf("limited %d of 5, want 3", limited)
	}
	if store.calls.Load() != 5 {
		t.Fatalf("store called %d times, want 5", store.calls.Load())
	}
	if got := counterValue(t, "ratelimit_store_errors_total", nil) - before; got != 5 {
		t.Fatalf("ratelimit_store_errors_total grew by %v, want 5", got)
	}
}

func TestLimiterMetrics(t *testing.T) {
	l := newTestLimiter(t, Config{Rules: []Rule{{Match: "/metrics.v1.M/*", Rate: "1/h", Burst: 2, Key: KeyIP}}})
	for range 5 {
		l.Allow(context.Background(), PreAuth, "/metrics.v1.M/Do", "7.7.7.7", "")
	}
	labels := map[string]string{"rule": "/metrics.v1.M/*", "key": "ip"}
	labels["result"] = "allowed"
	allowed := counterValue(t, "ratelimit_requests_total", labels)
	labels["result"] = "limited"
	limited := counterValue(t, "ratelimit_requests_total", labels)
	if allowed != 2 || limited != 3 {
		t.Fatalf("allowed %v limited %v, want 2 and 3", allowed, limited)
	}
}

// Two limiters sharing one store behave like two replicas sharing Redis: the
// limit holds for their combined traffic.
func TestLimiterSharedStoreAcrossInstances(t *testing.T) {
	shared := NewMemoryStore()
	cfg := Config{Default: &Rule{Rate: "1/h", Burst: 20}}
	a := newTestLimiter(t, cfg, WithStore(shared))
	b := newTestLimiter(t, cfg, WithStore(shared))
	var allowed atomic.Int64
	var wg sync.WaitGroup
	for i := range 8 {
		l := a
		if i%2 == 1 {
			l = b
		}
		wg.Go(func() {
			for range 50 {
				if !l.Allow(context.Background(), PostAuth, "/x.v1.X/Do", "", "alice").Limited {
					allowed.Add(1)
				}
			}
		})
	}
	wg.Wait()
	if got := allowed.Load(); got != 20 {
		t.Fatalf("two instances allowed %d requests together, want 20", got)
	}
}

func TestRateLimiterConcurrent(t *testing.T) {
	rl := NewRateLimiter(time.Minute, 10, 100)
	defer rl.Stop()
	var allowed atomic.Int64
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 50 {
				if rl.Allow("k") {
					allowed.Add(1)
				}
			}
		})
	}
	wg.Wait()
	if got := allowed.Load(); got != 10 {
		t.Fatalf("allowed %d, want 10", got)
	}
	rl.Reset("k")
	if !rl.Allow("k") {
		t.Fatal("Reset did not clear the key")
	}
}

func BenchmarkLimiterAllow(b *testing.B) {
	l, _ := New(Config{
		Default: &Rule{Rate: "1000000/s"},
		Rules: []Rule{
			{Match: "/auth.v1.AuthService/*", Rate: "5/s", Key: KeyIP},
			{Match: "/note.v1.NoteService/WatchNotes", Rate: "1/s"},
		},
	})
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		l.Allow(ctx, PostAuth, "/note.v1.NoteService/GetNote", "198.51.100.9", "alice")
	}
}

// gcraCases is testdata/gcra_cases.json: the language-neutral definition of
// the token bucket. The Redis script is checked against the same file in
// examples/internal/app (it needs a Redis).
type gcraCases struct {
	BaseTime int64 `json:"base_time"`
	Cases    []struct {
		Name     string `json:"name"`
		Interval int64  `json:"interval"`
		Burst    int    `json:"burst"`
		Steps    []struct {
			At         int64  `json:"at"`
			N          int    `json:"n"`
			Allowed    bool   `json:"allowed"`
			Remaining  int64  `json:"remaining"`
			RetryAfter int64  `json:"retry_after"`
			ResetAfter int64  `json:"reset_after"`
			TAT        *int64 `json:"tat"` // null: no key
		} `json:"steps"`
	} `json:"cases"`
}

func TestGCRACases(t *testing.T) {
	raw, err := os.ReadFile("testdata/gcra_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc gcraCases
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range doc.Cases {
		t.Run(c.Name, func(t *testing.T) {
			var tat int64
			exists := false // no key yet
			for i, s := range c.Steps {
				next, allowed, remaining, retry, reset := GCRA(tat, doc.BaseTime+s.At, c.Interval, c.Burst, s.N)
				if allowed || (exists && next != tat) {
					tat, exists = next, true // a store writes the result whenever it differs
				}
				if allowed != s.Allowed || remaining != s.Remaining || retry != s.RetryAfter || reset != s.ResetAfter {
					t.Fatalf("step %d (at %d, n %d): got allowed=%v remaining=%d retry=%d reset=%d, want %+v",
						i, s.At, s.N, allowed, remaining, retry, reset, s)
				}
				if exists != (s.TAT != nil) || (exists && tat-doc.BaseTime != *s.TAT) {
					t.Fatalf("step %d: stored value %d (exists %v), want %v", i, tat-doc.BaseTime, exists, s.TAT)
				}
			}
		})
	}
}

// A wall clock that steps backwards must not lock a key out for the size of
// the step: the bucket is treated as empty and refills at its normal rate.
func TestMemoryStoreClockStepBack(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	m := newMemoryStore(clock.Now)
	limit := Limit{Rate: 1, Period: time.Second, Burst: 2}
	ctx := context.Background()
	for range 2 {
		if res, _ := m.Take(ctx, "k", limit, 1); !res.Allowed {
			t.Fatal("burst must pass")
		}
	}
	clock.Advance(-time.Hour)
	res, _ := m.Take(ctx, "k", limit, 1)
	if res.Allowed || res.RetryAfter != time.Second {
		t.Fatalf("right after the step: %+v, want denied with a one-interval wait", res)
	}
	clock.Advance(time.Second)
	if res, _ = m.Take(ctx, "k", limit, 1); !res.Allowed {
		t.Fatalf("one interval after the step: %+v, want allowed (not locked out for an hour)", res)
	}
}

// Limits the integer arithmetic cannot represent are configuration errors.
func TestParseLimitRejectsUnrepresentable(t *testing.T) {
	for _, tt := range []struct {
		rate  string
		burst int
	}{
		{"2000000/s", 0},             // below one microsecond per token
		{"1/d", 4000},                // 4000 days to refill
		{"1/1000000h", 0},            // period itself beyond the bound
		{"1/h", 1 << 40},             // interval x burst overflows int64
		{"1000000/s", 1 << 62},       // burst overflow with the smallest interval
		{"9223372036854775807/s", 0}, // count overflow
	} {
		if l, err := ParseLimit(tt.rate, tt.burst); err == nil {
			t.Errorf("ParseLimit(%q, %d) = %v, want an error", tt.rate, tt.burst, l)
		}
	}
	// The bound is generous: a daily quota with a large burst is fine.
	if _, err := ParseLimit("1000/d", 100000); err != nil {
		t.Errorf("1000/d burst 100000: %v", err)
	}
}

// An IPv6 client is limited by its /64 (it can rotate freely inside it);
// ipv6Prefix changes that, IPv4 is always per address.
func TestLimiterIPv6Prefix(t *testing.T) {
	ctx := context.Background()
	l := newTestLimiter(t, Config{Default: &Rule{Rate: "1/h", Burst: 2, Key: KeyIP}})
	allowed := func(l *Limiter, addr string) bool { return !l.Allow(ctx, PreAuth, "/x", addr, "").Limited }
	if !allowed(l, "2001:db8:1:2::1") || !allowed(l, "2001:db8:1:2:ffff::9") || allowed(l, "2001:db8:1:2:abcd::1") {
		t.Fatal("addresses of one /64 must share a bucket of 2")
	}
	if !allowed(l, "2001:db8:1:3::1") {
		t.Fatal("another /64 has its own bucket")
	}
	if !allowed(l, "192.0.2.1") || !allowed(l, "192.0.2.2") || !allowed(l, "192.0.2.1") || allowed(l, "192.0.2.1") {
		t.Fatal("IPv4 addresses are limited one by one")
	}
	for i, want := range []bool{true, true, false} {
		if got := allowed(l, "::ffff:192.0.2.9"); got != want {
			t.Fatalf("IPv4-mapped address, request %d: allowed %v, want %v (it is an IPv4 address)", i+1, got, want)
		}
	}
	single := newTestLimiter(t, Config{Default: &Rule{Rate: "1/h", Burst: 1, Key: KeyIP}, IPv6Prefix: 128})
	if !allowed(single, "2001:db8:1:2::1") || !allowed(single, "2001:db8:1:2::2") || allowed(single, "2001:db8:1:2::1") {
		t.Fatal("ipv6Prefix 128 limits each address on its own")
	}
	if _, err := New(Config{Default: &Rule{Rate: "1/s"}, IPv6Prefix: 200}); err == nil {
		t.Fatal("ipv6Prefix 200: expected an error")
	}
}

// By default no forwarded header is believed: a caller, whatever its own
// address, cannot choose its bucket. Trust is explicit.
func TestLimiterClientIPTrustIsExplicit(t *testing.T) {
	def := newTestLimiter(t, Config{Default: &Rule{Rate: "1/s"}})
	for _, peer := range []string{"127.0.0.1:1234", "10.0.0.7:1234", "198.51.100.9:1234"} {
		want := peer[:len(peer)-5]
		if got := def.ClientIP(peer, []string{"203.0.113.50"}); got != want {
			t.Errorf("default: ClientIP(%s) = %s, want the peer %s", peer, got, want)
		}
	}
	private := newTestLimiter(t, Config{Default: &Rule{Rate: "1/s"}, TrustedProxies: []string{"private"}})
	if got := private.ClientIP("10.0.0.7:1234", []string{"203.0.113.50"}); got != "203.0.113.50" {
		t.Errorf("trustedProxies private: %s", got)
	}
	cidr := newTestLimiter(t, Config{Default: &Rule{Rate: "1/s"}, TrustedProxies: []string{"10.0.0.0/24"}})
	if got := cidr.ClientIP("10.0.1.7:1234", []string{"203.0.113.50"}); got != "10.0.1.7" {
		t.Errorf("peer outside the trusted CIDR: %s", got)
	}
}

// keyRecorder records the bucket keys a Limiter uses.
type keyRecorder struct {
	Store
	mu   sync.Mutex
	keys []string
}

func (k *keyRecorder) Take(ctx context.Context, key string, limit Limit, n int) (Result, error) {
	k.mu.Lock()
	k.keys = append(k.keys, key)
	k.mu.Unlock()
	return k.Store.Take(ctx, key, limit, n)
}

// Two limiters with different limits for the same rule on one store (a
// rolling deploy that changes a burst, two services on one prefix) must not
// refund each other's bucket: each limit has its own bucket, so the total a
// caller gets is bounded and does not grow with the number of alternations.
func TestChangedLimitStartsAFreshBucket(t *testing.T) {
	shared := &keyRecorder{Store: NewMemoryStore()}
	big := newTestLimiter(t, Config{Default: &Rule{Rate: "1/h", Burst: 100}}, WithStore(shared))
	small := newTestLimiter(t, Config{Default: &Rule{Rate: "1/h", Burst: 10}}, WithStore(shared))
	ctx := context.Background()
	var allowed int
	for range 5 {
		for range 150 {
			if !big.Allow(ctx, PostAuth, "/x.v1.X/Do", "", "alice").Limited {
				allowed++
			}
		}
		for range 150 {
			if !small.Allow(ctx, PostAuth, "/x.v1.X/Do", "", "alice").Limited {
				allowed++
			}
		}
	}
	if allowed != 110 {
		t.Fatalf("1500 requests alternating between burst 100 and burst 10 on one store: %d allowed, want 110 (100 + 10, once)", allowed)
	}
	// The key format is part of the cross-language contract.
	want := map[string]bool{
		"ratelimit:default|subject|alice#3600000000:100": true,
		"ratelimit:default|subject|alice#3600000000:10":  true,
	}
	for _, k := range shared.keys {
		if !want[k] {
			t.Fatalf("unexpected bucket key %q", k)
		}
	}
}

// trustedProxies is validated although nothing is limited: the server uses
// the list for the client address it logs and hands to handlers.
func TestTrustedProxiesValidatedWhenOff(t *testing.T) {
	for _, cfg := range []Config{
		{Disabled: true, TrustedProxies: []string{"gateway.internal"}},
		{TrustedProxies: []string{"none", "private"}},
	} {
		if l, err := New(cfg); err == nil || l != nil {
			t.Errorf("New(%+v) = %v, %v; want an error", cfg, l, err)
		}
	}
	if l, err := New(Config{Disabled: true, TrustedProxies: []string{"10.0.0.0/8"}}); err != nil || l != nil {
		t.Errorf("valid list, disabled: %v, %v; want nil, nil", l, err)
	}
}
