package grpcmux

import (
	"context"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	pbhealth "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// Creating several clients with metrics (the default) must not panic on
// duplicate Prometheus registration.
func TestNewClientConnTwice(t *testing.T) {
	for range 2 {
		conn, err := NewClientConn(context.Background(), "dns://127.0.0.1:1")
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.(*grpc.ClientConn).Close()
	}
}

// flakyService fails with UNAVAILABLE until it has been called failures times.
type flakyService struct {
	testService
	failures int32
	calls    atomic.Int32
}

func (f *flakyService) Check(ctx context.Context, in *validatedRequest) (*pbhealth.HealthCheckResponse, error) {
	if f.calls.Add(1) <= f.failures {
		return nil, status.Error(codes.Unavailable, "try again")
	}
	return f.testService.Check(ctx, in)
}

func dialTest(t *testing.T, s *Server, query string) *grpc.ClientConn {
	t.Helper()
	conn, err := NewClientConn(context.Background(), "dns://"+s.opts.grpcAddr+query)
	if err != nil {
		t.Fatal(err)
	}
	cc := conn.(*grpc.ClientConn)
	t.Cleanup(func() { _ = cc.Close() })
	return cc
}

func TestClientRetries(t *testing.T) {
	for _, mode := range []string{"interceptor", "native"} {
		t.Run(mode, func(t *testing.T) {
			s := newTestServer(t)
			svc := &flakyService{failures: 2}
			s.RegisterService(&testServiceDesc, svc)
			runTestServer(t, s)
			query := "?try=3&metrics=false"
			if mode == "native" {
				query += "&retryPolicy=native"
			}
			cc := dialTest(t, s, query)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out := &pbhealth.HealthCheckResponse{}
			if err := cc.Invoke(ctx, testCheckPath, &pbhealth.HealthCheckRequest{}, out); err != nil {
				t.Fatalf("after retries: %v", err)
			}
			if n := svc.calls.Load(); n != 3 {
				t.Fatalf("calls = %d, want 3", n)
			}
		})
	}
}

// waitForReady=true makes calls wait for the server instead of failing fast.
func TestClientWaitForReady(t *testing.T) {
	s := newTestServer(t)
	s.RegisterService(&testServiceDesc, &testService{})
	cc := dialTest(t, s, "?waitForReady=true&metrics=false&keepalive=20s")
	go func() {
		time.Sleep(300 * time.Millisecond)
		runTestServer(t, s)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out := &pbhealth.HealthCheckResponse{}
	if err := cc.Invoke(ctx, testCheckPath, &pbhealth.HealthCheckRequest{}, out); err != nil {
		t.Fatalf("waitForReady: %v", err)
	}
}

func TestClientServiceConfig(t *testing.T) {
	cases := []struct {
		lb, retry string
		try       uint64
		want      string
	}{
		{"", "", 0, `{"loadBalancingConfig":[{"round_robin":{}}]}`},
		{"pick_first", "", 0, `{"loadBalancingConfig":[{"pick_first":{}}]}`},
		{"", "native", 3, `{"loadBalancingConfig":[{"round_robin":{}}],"methodConfig":[{"name":[{}],"retryPolicy":{"maxAttempts":4,"initialBackoff":"0.1s","maxBackoff":"2s","backoffMultiplier":2,"retryableStatusCodes":["UNAVAILABLE"]}}]}`},
		{"", "native", 9, `{"loadBalancingConfig":[{"round_robin":{}}],"methodConfig":[{"name":[{}],"retryPolicy":{"maxAttempts":5,"initialBackoff":"0.1s","maxBackoff":"2s","backoffMultiplier":2,"retryableStatusCodes":["UNAVAILABLE"]}}]}`},
	}
	for _, tc := range cases {
		if got := serviceConfig(tc.lb, tc.retry == "native", tc.try); got != tc.want {
			t.Errorf("serviceConfig(%q, %q, %d)\n got %s\nwant %s", tc.lb, tc.retry, tc.try, got, tc.want)
		}
	}
}

func TestUseTLS(t *testing.T) {
	cases := map[string]bool{
		"dns://svc:8081":               false, // used to mean TLS because "dns" ends in "s"
		"dns://svc:8081?secure=true":   true,
		"grpcs://svc:443":              true,
		"https://svc:443":              true,
		"https://svc:443?secure=false": false,
		"http://svc:80":                false,
		"unix:///run/app.sock":         false,
	}
	for raw, want := range cases {
		u, _ := url.Parse(raw)
		if got := useTLS(u); got != want {
			t.Errorf("useTLS(%s) = %v, want %v", raw, got, want)
		}
	}
}

func TestGRPCTarget(t *testing.T) {
	cases := []struct{ uri, target, prefix string }{
		{"dns://svc.ns:8081", "svc.ns:8081", ""},
		{"dns://svc.ns:8081/", "svc.ns:8081", ""},
		{"dns://svc.ns:8081/api/", "svc.ns:8081", "/api"},
		{"dns:///svc.ns:8081", "dns:///svc.ns:8081", ""},
		{"unix:///run/app.sock", "unix:///run/app.sock", ""},
		{"polaris:///Production/svc:8081", "polaris:///Production/svc:8081", ""},
	}
	for _, tc := range cases {
		u, _ := url.Parse(tc.uri)
		target, prefix := grpcTarget(u)
		if target != tc.target || prefix != tc.prefix {
			t.Errorf("grpcTarget(%s) = %q, %q; want %q, %q", tc.uri, target, prefix, tc.target, tc.prefix)
		}
	}
}
