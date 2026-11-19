package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"github.com/alicebob/miniredis/v2"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/ti/common-go/dependencies/redis"
	userv1 "github.com/ti/common-go/examples/gen/go/example/user/v1"
	"github.com/ti/common-go/examples/internal/app"
	"github.com/ti/common-go/grpcmux"
	"github.com/ti/common-go/grpcmux/mux"
	"github.com/ti/common-go/tools/ratelimit"
)

// Rates of "1/h" make the tests independent of timing: the burst is all a
// caller gets while a test runs.

func limited(cfg ratelimit.Config) func(*app.Config) {
	return func(c *app.Config) { c.Apis.RateLimit = &cfg }
}

// A limited REST call is HTTP 429 with Retry-After and a body that says why,
// and a call is counted exactly once.
func TestRateLimitREST(t *testing.T) {
	e := startWith(t, limited(ratelimit.Config{Default: &ratelimit.Rule{Rate: "1/h", Burst: 3}}))
	for i := range 3 {
		if resp := e.call(t, http.MethodGet, "/v1/users", "", nil, nil); resp.StatusCode != http.StatusOK {
			t.Fatalf("request %d: %d, want 200 (burst is 3)", i+1, resp.StatusCode)
		}
	}
	var re restError
	resp := e.call(t, http.MethodGet, "/v1/users", "", nil, &re)
	if resp.StatusCode != http.StatusTooManyRequests || re.Error != "resource_exhausted" || re.ErrorCode != 8 {
		t.Fatalf("fourth request: %d %+v", resp.StatusCode, re)
	}
	secs, err := strconv.Atoi(resp.Header.Get("Retry-After"))
	if err != nil || secs < 1 || secs > 3600 {
		t.Fatalf("Retry-After %q", resp.Header.Get("Retry-After"))
	}
	var retry, info bool
	for _, d := range re.Details {
		switch d.Type {
		case "type.googleapis.com/google.rpc.RetryInfo":
			retry = strings.HasSuffix(d.RetryDelay, "s")
		case "type.googleapis.com/google.rpc.ErrorInfo":
			// Anonymous callers are limited by client IP.
			info = d.Reason == "RATE_LIMIT_EXCEEDED" && d.Domain == "ratelimit" &&
				d.Metadata["rule"] == "default" && d.Metadata["key"] == "ip"
		}
	}
	if !retry || !info {
		t.Fatalf("details: %+v", re.Details)
	}
	// Probes are never limited.
	for range 5 {
		if resp := e.call(t, http.MethodGet, "/readyz", "", nil, nil); resp.StatusCode != http.StatusOK {
			t.Fatalf("readyz: %d", resp.StatusCode)
		}
	}
}

func reasonOf(details []*connect.ErrorDetail) (reason string, retry bool) {
	for _, d := range details {
		msg, err := connectproto.UnmarshalErrorDetail(d)
		if err != nil {
			continue
		}
		switch m := msg.(type) {
		case *errdetails.ErrorInfo:
			reason = m.GetReason()
		case *errdetails.RetryInfo:
			retry = m.GetRetryDelay().AsDuration() > 0
		}
	}
	return reason, retry
}

// The same limit answers each protocol in its own form: a Connect error, a
// gRPC status with a retry-after header, and for streams an error when the
// stream is opened.
func TestRateLimitConnectAndGRPC(t *testing.T) {
	e := startWith(t, limited(ratelimit.Config{Rules: []ratelimit.Rule{
		{Match: "/example.user.v1.UserService/*", Rate: "1/h", Burst: 2, PerMethod: true},
	}}))
	ctx := t.Context()

	// Connect unary (HTTP port).
	for i := range 2 {
		if _, err := e.connect.ListUsers(ctx, &userv1.ListUsersRequest{}); err != nil {
			t.Fatalf("Connect call %d: %v", i+1, err)
		}
	}
	_, err := e.connect.ListUsers(ctx, &userv1.ListUsersRequest{})
	var cerr *connect.Error
	if !errors.As(err, &cerr) || cerr.Code() != connect.CodeResourceExhausted {
		t.Fatalf("Connect third call: %v", err)
	}
	if reason, retry := reasonOf(cerr.Details()); reason != "RATE_LIMIT_EXCEEDED" || !retry {
		t.Fatalf("Connect details: reason %q retry %v", reason, retry)
	}

	// Connect server streaming (HTTP port): a stream counts when it opens,
	// and the refusal arrives as the stream's error.
	for i := range 2 {
		st, err := e.connect.ExportUsers(ctx, &userv1.ExportUsersRequest{})
		if err != nil {
			t.Fatalf("Connect stream %d: %v", i+1, err)
		}
		for {
			if _, err := st.Receive(); err != nil {
				break
			}
		}
		_ = st.Close()
	}
	st, err := e.connect.ExportUsers(ctx, &userv1.ExportUsersRequest{})
	if err == nil {
		_, err = st.Receive()
		_ = st.Close()
	}
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("Connect third stream: %v", err)
	}

	// Native gRPC (gRPC port) has its own counters per method, shared with
	// the HTTP port: GetUser has not been called yet.
	for i := range 2 {
		if _, err := e.grpc.GetUser(ctx, &userv1.GetUserRequest{UserId: "1"}); status.Code(err) != codes.NotFound {
			t.Fatalf("gRPC call %d: %v", i+1, err)
		}
	}
	var header metadata.MD
	_, err = e.grpc.GetUser(ctx, &userv1.GetUserRequest{UserId: "1"}, grpc.Header(&header))
	gst := status.Convert(err)
	if gst.Code() != codes.ResourceExhausted || len(header.Get("retry-after")) != 1 {
		t.Fatalf("gRPC third call: %v, retry-after %v", err, header.Get("retry-after"))
	}
	var reason string
	for _, d := range gst.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok {
			reason = info.GetReason()
		}
	}
	if reason != "RATE_LIMIT_EXCEEDED" {
		t.Fatalf("gRPC details: %v", gst.Details())
	}
	// The limit is per caller and method on both ports together: ListUsers
	// was used up over Connect, so gRPC is refused as well.
	if _, err := e.grpc.ListUsers(ctx, &userv1.ListUsersRequest{}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("gRPC ListUsers after Connect used the budget: %v", err)
	}
}

type testAuthInfo struct {
	mux.UnimplementedAuthInfo
	user string
}

func (a testAuthInfo) GetUserID() string { return a.user }

// bearerIsUser authenticates "Bearer <user>"; anything else is rejected.
func bearerIsUser(ctx context.Context) (context.Context, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	v := md.Get("authorization")
	user, ok := "", false
	if len(v) > 0 {
		user, ok = strings.CutPrefix(v[0], "Bearer user-")
	}
	if !ok || user == "" {
		return ctx, status.Error(codes.Unauthenticated, "missing or invalid token")
	}
	return mux.NewAuthInfoContext(ctx, testAuthInfo{user: user}), nil
}

// Authenticated callers have a budget each, whatever address they come from.
func TestRateLimitBySubject(t *testing.T) {
	e := startWith(t, limited(ratelimit.Config{Default: &ratelimit.Rule{Rate: "1/h", Burst: 2}}),
		grpcmux.WithAuthFunc(bearerIsUser))
	as := func(user string) http.Header { return http.Header{"Authorization": {"Bearer user-" + user}} }
	for i := range 2 {
		if resp := e.call(t, http.MethodGet, "/v1/users", "", as("alice"), nil); resp.StatusCode != http.StatusOK {
			t.Fatalf("alice %d: %d", i+1, resp.StatusCode)
		}
	}
	var re restError
	if resp := e.call(t, http.MethodGet, "/v1/users", "", as("alice"), &re); resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("alice third: %d", resp.StatusCode)
	}
	for _, d := range re.Details {
		if d.Reason != "" && d.Metadata["key"] != "subject" {
			t.Fatalf("limited by %q, want subject", d.Metadata["key"])
		}
	}
	// Bob shares alice's IP but not her bucket.
	if resp := e.call(t, http.MethodGet, "/v1/users", "", as("bob"), nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("bob: %d", resp.StatusCode)
	}
	// Requests rejected by authentication never reach a subject bucket.
	for range 5 {
		if resp := e.call(t, http.MethodGet, "/v1/users", "", nil, nil); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("anonymous: %d, want 401", resp.StatusCode)
		}
	}
}

// A rule keyed by IP runs before authentication: it bounds a public sign-in
// method, and attempts with bad credentials on a protected one.
func TestRateLimitPreAuthByIP(t *testing.T) {
	e := startWith(t, limited(ratelimit.Config{Rules: []ratelimit.Rule{
		{Match: "/example.user.v1.UserService/GetUser", Rate: "1/h", Burst: 2, Key: "ip"},
		{Match: "/example.user.v1.UserService/ListUsers", Rate: "1/h", Burst: 2, Key: "ip"},
	}}),
		grpcmux.WithAuthFunc(bearerIsUser),
		grpcmux.WithNoAuthMethods("/example.user.v1.UserService/ListUsers"),
	)
	bad := http.Header{"Authorization": {"Bearer wrong"}}
	// Protected method, bad credentials: 401 twice, then 429 without the
	// credentials being looked at again.
	for i, want := range []int{http.StatusUnauthorized, http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusTooManyRequests} {
		if resp := e.call(t, http.MethodGet, "/v1/users/1", "", bad, nil); resp.StatusCode != want {
			t.Fatalf("GetUser attempt %d: %d, want %d", i+1, resp.StatusCode, want)
		}
	}
	// Public method (exempt from authentication), same limit.
	for i, want := range []int{http.StatusOK, http.StatusOK, http.StatusTooManyRequests} {
		if resp := e.call(t, http.MethodGet, "/v1/users", "", nil, nil); resp.StatusCode != want {
			t.Fatalf("ListUsers call %d: %d, want %d", i+1, resp.StatusCode, want)
		}
	}
	// Forwarded headers are not believed unless a trusted proxy is
	// configured: the caller stays in its bucket whatever it sends.
	fwd := http.Header{"X-Forwarded-For": {"198.51.100.77"}}
	if resp := e.call(t, http.MethodGet, "/v1/users", "", fwd, nil); resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("forged X-Forwarded-For escaped the limit: %d", resp.StatusCode)
	}
}

// With a Redis dependency two replicas share their buckets: the limit holds
// for their combined traffic. The Redis is miniredis, which runs the real
// Lua script.
func TestRateLimitSharedAcrossReplicas(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	stopRedis := sync.OnceFunc(mr.Close)
	t.Cleanup(stopRedis)
	const burst = 10
	replica := func() *env {
		rdb, err := redis.New(t.Context(), "redis://"+mr.Addr())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = rdb.Close(context.Background()) })
		return startWith(t, func(c *app.Config) {
			c.Dependencies.Redis = rdb
			c.Apis.RateLimit = &ratelimit.Config{Default: &ratelimit.Rule{Rate: "1/h", Burst: burst}, KeyPrefix: "example"}
		})
	}
	replicas := []*env{replica(), replica()}

	var ok, tooMany, other atomic.Int64
	var wg sync.WaitGroup
	for i := range 8 {
		e := replicas[i%2]
		wg.Go(func() {
			for range 10 {
				switch resp := e.call(t, http.MethodGet, "/v1/users", "", nil, nil); resp.StatusCode {
				case http.StatusOK:
					ok.Add(1)
				case http.StatusTooManyRequests:
					tooMany.Add(1)
				default:
					other.Add(1)
				}
			}
		})
	}
	wg.Wait()
	if ok.Load() != burst || tooMany.Load() != 80-burst || other.Load() != 0 {
		t.Fatalf("two replicas: %d allowed, %d limited, %d other; want %d, %d, 0",
			ok.Load(), tooMany.Load(), other.Load(), burst, 80-burst)
	}
	// One bucket, in Redis, under the configured prefix.
	keys := mr.Keys()
	if len(keys) != 1 || !strings.HasPrefix(keys[0], "example:default|ip|") {
		t.Fatalf("redis keys: %v", keys)
	}
	if ttl := mr.TTL(keys[0]); ttl <= 0 {
		t.Fatalf("bucket key has no expiry (ttl %s)", ttl)
	}

	// Redis goes away: the replicas keep limiting, each on its own memory.
	stopRedis()
	var okAfter int
	for range burst + 5 {
		if resp := replicas[0].call(t, http.MethodGet, "/v1/users", "", nil, nil); resp.StatusCode == http.StatusOK {
			okAfter++
		}
	}
	if okAfter != burst {
		t.Fatalf("during the outage one replica allowed %d requests, want its in-memory burst of %d", okAfter, burst)
	}
}

// The Lua script (tools/ratelimit/gcra.lua, the file every backend loads) is
// run against the language-neutral cases that also pin ratelimit.GCRA, on a
// Redis whose clock the test controls.
func TestRedisScriptMatchesGCRACases(t *testing.T) {
	raw, err := os.ReadFile("../../../tools/ratelimit/testdata/gcra_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		BaseTime int64 `json:"base_time"`
		Cases    []struct {
			Name     string `json:"name"`
			Interval int64  `json:"interval"`
			Burst    int    `json:"burst"`
			Steps    []struct {
				At         int64  `json:"at"`
				N          int    `json:"n"`
				Allowed    bool   `json:"allowed"`
				Remaining  int    `json:"remaining"`
				RetryAfter int64  `json:"retry_after"`
				ResetAfter int64  `json:"reset_after"`
				TAT        *int64 `json:"tat"` // null: no key
			} `json:"steps"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mr.Close)
	rdb, err := redis.New(t.Context(), "redis://"+mr.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rdb.Close(context.Background()) })
	store := rdb.RateLimiter()

	for i, c := range doc.Cases {
		key := "case-" + strconv.Itoa(i)
		limit := ratelimit.Limit{Rate: 1, Period: time.Duration(c.Interval) * time.Microsecond, Burst: c.Burst}
		for j, s := range c.Steps {
			mr.SetTime(time.UnixMicro(doc.BaseTime + s.At))
			res, err := store.Take(t.Context(), key, limit, s.N)
			if err != nil {
				t.Fatalf("%s step %d: %v", c.Name, j, err)
			}
			if res.Allowed != s.Allowed || res.Remaining != s.Remaining ||
				res.RetryAfter.Microseconds() != s.RetryAfter || res.ResetAfter.Microseconds() != s.ResetAfter {
				t.Fatalf("%s step %d: got %+v, want %+v", c.Name, j, res, s)
			}
			// The stored value is the time at which the bucket is full again.
			switch stored, _ := mr.Get(key); {
			case s.TAT == nil:
				if mr.Exists(key) {
					t.Fatalf("%s step %d: a key was written, want none", c.Name, j)
				}
			case stored != strconv.FormatInt(doc.BaseTime+*s.TAT, 10):
				t.Fatalf("%s step %d: stored %q, want %d", c.Name, j, stored, doc.BaseTime+*s.TAT)
			}
		}
	}
}
