package grpcmux

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	pbhealth "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/ti/common-go/tools/ratelimit"
)

// countingStore counts the bucket operations a server performs.
type countingStore struct {
	ratelimit.Store
	takes atomic.Int32
}

func (c *countingStore) Take(ctx context.Context, key string, limit ratelimit.Limit, n int) (ratelimit.Result, error) {
	c.takes.Add(1)
	return c.Store.Take(ctx, key, limit, n)
}

func limitedServer(t *testing.T, cfg ratelimit.Config, opts ...Option) (*Server, *countingStore) {
	t.Helper()
	store := &countingStore{Store: ratelimit.NewMemoryStore()}
	s, _ := connectServer(t, append([]Option{WithRateLimit(cfg), WithRateLimitStore(store)}, opts...)...)
	return s, store
}

func rawPost(t *testing.T, s *Server, path, contentType string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+s.opts.httpAddr+path, strings.NewReader("{}"))
	req.Header.Set("Content-Type", contentType)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

// One limit, every protocol of the HTTP port: each client reads the refusal
// in its own wire format, and every request costs exactly one bucket
// operation.
func TestRateLimitHTTPProtocols(t *testing.T) {
	s, store := limitedServer(t, ratelimit.Config{Default: &ratelimit.Rule{Rate: "1/h", Burst: 1}})
	ctx := t.Context()

	if _, _, err := connectClient(s, testCheckPath).call(ctx, &pbhealth.HealthCheckRequest{}); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if store.takes.Load() != 1 {
		t.Fatalf("first call cost %d bucket operations, want 1", store.takes.Load())
	}

	// Connect unary: the Connect error with HTTP 429.
	_, _, err := connectClient(s, testCheckPath).call(ctx, &pbhealth.HealthCheckRequest{})
	cerr, ok := errors.AsType[*connect.Error](err)
	if !ok || cerr.Code() != connect.CodeResourceExhausted || len(cerr.Details()) != 2 {
		t.Fatalf("Connect: %v", err)
	}
	resp, body := rawPost(t, s, testCheckPath, "application/json")
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") == "" ||
		!strings.Contains(string(body), `"code":"resource_exhausted"`) {
		t.Fatalf("Connect unary raw: %d %s", resp.StatusCode, body)
	}

	// gRPC and gRPC-Web on the HTTP port: HTTP 200, the status in the
	// (trailers-only) headers.
	for _, ct := range []string{"application/grpc", "application/grpc-web+proto"} {
		resp, _ = rawPost(t, s, testCheckPath, ct)
		// connect-go sends the status as headers (trailers-only) or as HTTP
		// trailers, depending on the protocol; a gRPC client reads both.
		meta := func(k string) string {
			if v := resp.Header.Get(k); v != "" {
				return v
			}
			return resp.Trailer.Get(k)
		}
		if resp.StatusCode != http.StatusOK || meta("Grpc-Status") != "8" ||
			meta("Grpc-Status-Details-Bin") == "" || resp.Header.Get("Retry-After") == "" {
			t.Fatalf("%s: %d headers %v trailers %v", ct, resp.StatusCode, resp.Header, resp.Trailer)
		}
	}

	// Connect streaming: HTTP 200 and an end-of-stream envelope (flag 0x02).
	resp, body = rawPost(t, s, testCheckPath, "application/connect+json")
	if resp.StatusCode != http.StatusOK || len(body) < 5 || body[0] != 0x02 ||
		!strings.Contains(string(body[5:]), `"code":"resource_exhausted"`) {
		t.Fatalf("Connect stream: %d %q", resp.StatusCode, body)
	}

	// Any other HTTP route: the REST error body, HTTP 429, Retry-After.
	resp, body = rawPost(t, s, "/v1/anything", "application/json")
	var re struct {
		Error   string `json:"error"`
		Details []struct {
			Type   string `json:"@type"`
			Reason string `json:"reason"`
		} `json:"details"`
	}
	if err := json.Unmarshal(body, &re); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	if resp.StatusCode != http.StatusTooManyRequests || resp.Header.Get("Retry-After") != "3600" ||
		re.Error != "resource_exhausted" || len(re.Details) != 2 || re.Details[1].Reason != ratelimit.Reason {
		t.Fatalf("REST: %d Retry-After %q %s", resp.StatusCode, resp.Header.Get("Retry-After"), body)
	}

	// 1 allowed + 6 refused requests = 7 bucket operations.
	if got := store.takes.Load(); got != 7 {
		t.Fatalf("7 requests cost %d bucket operations", got)
	}
	// Probes and CORS preflights cost nothing.
	if code := httpGetStatus(t, "http://"+s.opts.httpAddr+"/healthz"); code != http.StatusOK {
		t.Fatalf("healthz: %d", code)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodOptions, "http://"+s.opts.httpAddr+testCheckPath, nil)
	req.Header.Set("Origin", "https://app.example.com")
	pre, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = pre.Body.Close()
	if pre.StatusCode != http.StatusOK || store.takes.Load() != 7 {
		t.Fatalf("preflight: %d, bucket operations %d", pre.StatusCode, store.takes.Load())
	}
}

// The gRPC port is limited by interceptors, with the same buckets as the HTTP
// port; calls through the in-memory connection are not limited (a gateway
// route calling back through Conn has already been counted by the HTTP
// middleware).
func TestRateLimitGRPCPortAndMemConn(t *testing.T) {
	s, store := limitedServer(t, ratelimit.Config{Default: &ratelimit.Rule{Rate: "1/h", Burst: 2}})
	ctx := t.Context()
	cc, err := grpc.NewClient(s.opts.grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cc.Close() }()
	out := &pbhealth.HealthCheckResponse{}

	if err := cc.Invoke(ctx, testCheckPath, &pbhealth.HealthCheckRequest{}, out); err != nil {
		t.Fatalf("gRPC first call: %v", err)
	}
	// The second token goes to a Connect call from the same address.
	if _, _, err := connectClient(s, testCheckPath).call(ctx, &pbhealth.HealthCheckRequest{}); err != nil {
		t.Fatalf("Connect call: %v", err)
	}
	var header metadata.MD
	err = cc.Invoke(ctx, testCheckPath, &pbhealth.HealthCheckRequest{}, out, grpc.Header(&header))
	st := status.Convert(err)
	if st.Code() != codes.ResourceExhausted || st.Message() != "rate limit exceeded" {
		t.Fatalf("gRPC third call: %v", err)
	}
	if v := header.Get("retry-after"); len(v) != 1 || v[0] != "3600" {
		t.Fatalf("retry-after header: %v", v)
	}
	var retry *errdetails.RetryInfo
	var info *errdetails.ErrorInfo
	for _, d := range st.Details() {
		switch m := d.(type) {
		case *errdetails.RetryInfo:
			retry = m
		case *errdetails.ErrorInfo:
			info = m
		}
	}
	if retry == nil || retry.GetRetryDelay().AsDuration() <= 59*time.Minute ||
		info == nil || info.GetReason() != ratelimit.Reason || info.GetDomain() != ratelimit.Domain ||
		info.GetMetadata()["rule"] != "default" || info.GetMetadata()["key"] != "ip" {
		t.Fatalf("details: %v", st.Details())
	}
	// The health service is never limited.
	if _, err := pbhealth.NewHealthClient(cc).Check(ctx, &pbhealth.HealthCheckRequest{}); err != nil {
		t.Fatalf("health: %v", err)
	}

	before := store.takes.Load()
	for range 5 {
		if err := s.Conn().Invoke(ctx, testCheckPath, &pbhealth.HealthCheckRequest{}, out); err != nil {
			t.Fatalf("in-memory call: %v", err)
		}
	}
	if store.takes.Load() != before {
		t.Fatalf("in-memory calls cost %d bucket operations, want 0", store.takes.Load()-before)
	}
}

// A rule keyed by IP runs before authentication on the gRPC port too.
func TestRateLimitGRPCPreAuth(t *testing.T) {
	var authCalls atomic.Int32
	deny := func(ctx context.Context) (context.Context, error) {
		authCalls.Add(1)
		return ctx, status.Error(codes.Unauthenticated, "no")
	}
	s, _ := limitedServer(t, ratelimit.Config{Rules: []ratelimit.Rule{
		{Match: "/" + testServiceName + "/*", Rate: "1/h", Burst: 2, Key: ratelimit.KeyIP},
	}}, WithAuthFunc(deny))
	cc, err := grpc.NewClient(s.opts.grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cc.Close() }()
	var got []codes.Code
	for range 4 {
		err := cc.Invoke(t.Context(), testCheckPath, &pbhealth.HealthCheckRequest{}, &pbhealth.HealthCheckResponse{})
		got = append(got, status.Code(err))
	}
	want := []codes.Code{codes.Unauthenticated, codes.Unauthenticated, codes.ResourceExhausted, codes.ResourceExhausted}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("codes %v, want %v", got, want)
		}
	}
	if authCalls.Load() != 2 {
		t.Fatalf("authentication ran %d times, want 2 (limited calls never reach it)", authCalls.Load())
	}
}

func TestMethodTimeouts(t *testing.T) {
	tm, err := newMethodTimeouts(&TimeoutConfig{
		Default: "10s",
		Rules: []TimeoutRule{
			{Match: "/a.v1.A/*", Timeout: "1m"},
			{Match: "/a.v1.A/Slow", Timeout: "none"},
			{Match: "/b.v1.B/Quick", Timeout: "250ms"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]time.Duration{
		"/c.v1.C/Any":   10 * time.Second,
		"/a.v1.A/Get":   time.Minute,
		"/a.v1.A/Slow":  0,
		"/a.v1.AB/Get":  10 * time.Second,
		"/b.v1.B/Quick": 250 * time.Millisecond,
		"/b.v1.B/Other": 10 * time.Second,
	}
	for method, want := range tests {
		for range 2 { // the second read comes from the cache
			if got := tm.forMethod(method); got != want {
				t.Errorf("forMethod(%s) = %s, want %s", method, got, want)
			}
		}
	}
	if tm, err := newMethodTimeouts(&TimeoutConfig{}); tm != nil || err != nil {
		t.Errorf("an empty config must mean no deadlines: %v %v", tm, err)
	}
	for _, bad := range []TimeoutConfig{
		{Default: "soon"},
		{Rules: []TimeoutRule{{Match: "/a.v1.A/*", Timeout: "-1s"}}},
		{Rules: []TimeoutRule{{Timeout: "1s"}}},
		{Rules: []TimeoutRule{{Match: "/a.v1.A*", Timeout: "1s"}}},
	} {
		if _, err := newMethodTimeouts(&bad); err == nil {
			t.Errorf("newMethodTimeouts(%+v): expected an error", bad)
		}
	}
}

// The deadline applies to unary calls on the gRPC port and on the HTTP
// bridges, a rule can lift it, and a bare context error becomes
// DEADLINE_EXCEEDED.
func TestTimeoutsOnEveryProtocol(t *testing.T) {
	s, _ := connectServer(t, WithTimeouts(TimeoutConfig{
		Default: "80ms",
		Rules:   []TimeoutRule{{Match: testMutatePath, Timeout: "none"}},
	}))
	ctx := t.Context()
	slow := &pbhealth.HealthCheckRequest{Service: "slow"} // blocks until its context ends

	start := time.Now()
	_, _, err := connectClient(s, testCheckPath).call(ctx, slow)
	if connect.CodeOf(err) != connect.CodeDeadlineExceeded || time.Since(start) > 2*time.Second {
		t.Fatalf("Connect: %v after %s", err, time.Since(start))
	}
	cc, err := grpc.NewClient(s.opts.grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cc.Close() }()
	err = cc.Invoke(ctx, testCheckPath, slow, &pbhealth.HealthCheckResponse{})
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("gRPC: %v", err)
	}
	// "sleep" takes 400ms: longer than the default, but Mutate is exempt.
	if err := cc.Invoke(ctx, testMutatePath, &pbhealth.HealthCheckRequest{Service: "sleep"}, &pbhealth.HealthCheckResponse{}); err != nil {
		t.Fatalf("exempt method: %v", err)
	}

	tm, _ := newMethodTimeouts(&TimeoutConfig{Default: "10ms"})
	_, err = timeoutUnaryInterceptor(tm)(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/x.v1.X/Do"},
		func(ctx context.Context, _ any) (any, error) {
			<-ctx.Done()
			return nil, ctx.Err() // no gRPC status
		})
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("bare context error: %v", err)
	}
}

// An IP-keyed limit must hold by default against a caller that sends a new
// X-Forwarded-For with every request, also from a private or loopback peer
// (behind an L4 load balancer or SNAT the header is the client's own). The
// header counts only for peers configured as trusted proxies.
func TestRateLimitIgnoresForwardedForByDefault(t *testing.T) {
	rule := ratelimit.Rule{Rate: "1/h", Burst: 1, Key: ratelimit.KeyIP}
	statuses := func(s *Server) []int {
		var got []int
		for i := range 5 {
			req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+s.opts.httpAddr+testCheckPath, strings.NewReader("{}"))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Forwarded-For", "203.0.113."+strconv.Itoa(10+i))
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			got = append(got, resp.StatusCode)
		}
		return got
	}

	def, _ := limitedServer(t, ratelimit.Config{Default: &rule})
	if got, want := statuses(def), []int{200, 429, 429, 429, 429}; !slices.Equal(got, want) {
		t.Fatalf("default trust: %v, want %v", got, want)
	}

	// The test client is on loopback; declared a trusted proxy, its header
	// names the client, and each of the five clients has its own bucket.
	trusted, _ := limitedServer(t, ratelimit.Config{Default: &rule, TrustedProxies: []string{"private"}})
	if got, want := statuses(trusted), []int{200, 200, 200, 200, 200}; !slices.Equal(got, want) {
		t.Fatalf("trustedProxies private: %v, want %v", got, want)
	}

	// Same on the gRPC port, where the header is metadata.
	cc, err := grpc.NewClient(def.opts.grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cc.Close() }()
	ctx := metadata.AppendToOutgoingContext(t.Context(), "x-forwarded-for", "203.0.113.99")
	if err := cc.Invoke(ctx, testCheckPath, &pbhealth.HealthCheckRequest{}, &pbhealth.HealthCheckResponse{}); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("gRPC with forged x-forwarded-for metadata: %v, want RESOURCE_EXHAUSTED", err)
	}
}
