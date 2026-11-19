package grpcmux

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	pbhealth "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"

	"github.com/ti/common-go/tools/ratelimit"
)

// forgedClientHeaders is everything a caller might send to pick the address
// it is seen as. None of it may be taken at its word.
var forgedClientHeaders = map[string]string{
	"X-Forwarded-For":           "1.2.3.4",
	"X-Real-IP":                 "1.2.3.5",
	"True-Client-IP":            "1.2.3.6",
	"CloudFront-Viewer-Address": "1.2.3.7:4711",
	"Forwarded":                 "for=1.2.3.8",
	"X-Envoy-External-Address":  "1.2.3.9",
	"X-Client-IP":               "1.2.3.10",
	"Ip":                        "1.2.3.11",
}

// seenClient records what the server hands to application code: ClientIP
// and the "ip" incoming metadata (the key of routerlimit rules on "ip").
type seenClient struct {
	mu       sync.Mutex
	clientIP string
	metaIP   []string
}

func (c *seenClient) authFunc(ctx context.Context) (context.Context, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	c.mu.Lock()
	c.clientIP, c.metaIP = ClientIP(ctx), md.Get("ip")
	c.mu.Unlock()
	return ctx, nil
}

func (c *seenClient) assert(t *testing.T, what, want string) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.clientIP != want {
		t.Errorf("%s: ClientIP = %q, want %q", what, c.clientIP, want)
	}
	if len(c.metaIP) != 1 || c.metaIP[0] != want {
		t.Errorf("%s: ip metadata = %q, want [%q]", what, c.metaIP, want)
	}
}

func postWithHeaders(t *testing.T, s *Server, headers map[string]string, forwardedFor ...string) {
	t.Helper()
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+s.opts.httpAddr+testCheckPath, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if len(forwardedFor) > 0 {
		req.Header["X-Forwarded-For"] = forwardedFor
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func grpcCheck(t *testing.T, s *Server, pairs ...string) {
	t.Helper()
	cc, err := grpc.NewClient(s.opts.grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cc.Close() }()
	ctx := metadata.AppendToOutgoingContext(t.Context(), pairs...)
	if err := cc.Invoke(ctx, testCheckPath, &pbhealth.HealthCheckRequest{}, &pbhealth.HealthCheckResponse{}); err != nil {
		t.Fatal(err)
	}
}

// A caller that is not a trusted proxy cannot choose the address the server
// records for it, on either port, with any of the headers proxies use. The
// test client connects from loopback, so that is the only right answer.
func TestClientIPCannotBeForgedByHeaders(t *testing.T) {
	const loopback = "127.0.0.1"
	for name, opts := range map[string][]Option{
		"no configuration":           nil,
		"rate limiting configured":   {WithRateLimit(ratelimit.Config{Default: &ratelimit.Rule{Rate: "1000/s"}})},
		"another network is trusted": {WithRateLimit(ratelimit.Config{TrustedProxies: []string{"203.0.113.0/24"}})},
	} {
		t.Run(name, func(t *testing.T) {
			seen := &seenClient{}
			s, _ := connectServer(t, append([]Option{WithAuthFunc(seen.authFunc)}, opts...)...)

			postWithHeaders(t, s, forgedClientHeaders)
			seen.assert(t, "HTTP, forged headers", loopback)
			postWithHeaders(t, s, nil, "1.2.3.4", "5.6.7.8, 9.9.9.9")
			seen.assert(t, "HTTP, several X-Forwarded-For lines", loopback)
			postWithHeaders(t, s, nil)
			seen.assert(t, "HTTP, no header", loopback)

			grpcCheck(t, s, "x-forwarded-for", "1.2.3.4", "x-real-ip", "1.2.3.5", "ip", "1.2.3.11")
			seen.assert(t, "gRPC, forged metadata", loopback)
			grpcCheck(t, s)
			seen.assert(t, "gRPC, no metadata", loopback)
		})
	}
}

// From a trusted proxy the client is the rightmost X-Forwarded-For entry that
// is not a trusted proxy: what the proxy appended, never what the client
// prepended. The list applies although no rate-limit rule is configured.
func TestClientIPFromTrustedProxy(t *testing.T) {
	seen := &seenClient{}
	s, _ := connectServer(t, WithAuthFunc(seen.authFunc),
		WithRateLimit(ratelimit.Config{TrustedProxies: []string{"private"}}))
	if s.opts.rateLimiter() != nil {
		t.Fatal("trustedProxies alone must not turn rate limiting on")
	}

	postWithHeaders(t, s, forgedClientHeaders, "1.2.3.4, 198.51.100.9, 10.0.0.5")
	seen.assert(t, "HTTP, client prepended a hop", "198.51.100.9")
	postWithHeaders(t, s, nil, "1.2.3.4", "2001:db8::7", "10.0.0.5, 100.64.3.1")
	seen.assert(t, "HTTP, several lines, IPv6 client", "2001:db8::7")
	postWithHeaders(t, s, nil, "1.2.3.4, not-an-address, 10.0.0.5")
	seen.assert(t, "HTTP, garbage stops the walk", "10.0.0.5")
	postWithHeaders(t, s, map[string]string{"X-Real-IP": "1.2.3.5"})
	seen.assert(t, "HTTP, X-Real-IP is never read", "127.0.0.1")

	grpcCheck(t, s, "x-forwarded-for", "1.2.3.4, 198.51.100.9", "ip", "1.2.3.11")
	seen.assert(t, "gRPC, client prepended a hop", "198.51.100.9")
}
