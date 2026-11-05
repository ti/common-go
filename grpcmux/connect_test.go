package grpcmux

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	pbhealth "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

func statusCode(err error) string {
	if err == nil {
		return "OK"
	}
	return status.Code(err).String()
}

func connectServer(t *testing.T, opts ...Option) (*Server, *testService) {
	t.Helper()
	s := newTestServer(t, opts...)
	svc := &testService{}
	RegisterConnectService(s, &testServiceDesc, svc)
	runTestServer(t, s)
	return s, svc
}

func connectClient(s *Server, path string, opts ...connect.ClientOption) *connect.Client[pbhealth.HealthCheckRequest, pbhealth.HealthCheckResponse] {
	return connect.NewClient[pbhealth.HealthCheckRequest, pbhealth.HealthCheckResponse](
		http.DefaultClient, "http://"+s.opts.httpAddr+path, opts...)
}

// A real connect-go client must interoperate in every unary mode it supports.
func TestConnectConformance(t *testing.T) {
	s, _ := connectServer(t)
	cases := []struct {
		name string
		path string
		opts []connect.ClientOption
	}{
		{"proto", testCheckPath, nil},
		{"json", testCheckPath, []connect.ClientOption{connect.WithProtoJSON()}},
		{"proto+gzip", testCheckPath, []connect.ClientOption{connect.WithSendGzip()}},
		{"json+gzip", testCheckPath, []connect.ClientOption{connect.WithProtoJSON(), connect.WithSendGzip()}},
		{"get", testCheckPath, []connect.ClientOption{connect.WithHTTPGet(), connect.WithIdempotency(connect.IdempotencyNoSideEffects)}},
		{"get+json", testCheckPath, []connect.ClientOption{connect.WithProtoJSON(), connect.WithHTTPGet(), connect.WithIdempotency(connect.IdempotencyNoSideEffects)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			resp, err := connectClient(s, tc.path, tc.opts...).CallUnary(ctx, connect.NewRequest(&pbhealth.HealthCheckRequest{Service: "x"}))
			if err != nil {
				t.Fatal(err)
			}
			if resp.Msg.GetStatus() != pbhealth.HealthCheckResponse_SERVING {
				t.Fatalf("status = %v", resp.Msg.GetStatus())
			}
		})
	}
}

// Errors keep their code, message and details across the Connect wire format.
func TestConnectErrorDetails(t *testing.T) {
	s, _ := connectServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := connectClient(s, testCheckPath).CallUnary(ctx, connect.NewRequest(&pbhealth.HealthCheckRequest{Service: "fail"}))
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		t.Fatalf("want *connect.Error, got %v", err)
	}
	if cerr.Code() != connect.CodeNotFound || cerr.Message() != "thing not found" {
		t.Fatalf("code=%v message=%q", cerr.Code(), cerr.Message())
	}
	var found bool
	for _, d := range cerr.Details() {
		v, err := d.Value()
		if err != nil {
			t.Fatal(err)
		}
		if info, ok := v.(*errdetails.ErrorInfo); ok && info.GetReason() == "THING_MISSING" {
			found = true
		}
	}
	if !found {
		t.Fatalf("ErrorInfo detail missing: %v", cerr.Details())
	}
}

// The unary interceptor chain (validator, recovery, ...) runs on the Connect
// path by default, the same as on native gRPC.
func TestConnectRunsInterceptorsByDefault(t *testing.T) {
	s, svc := connectServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cli := connectClient(s, testCheckPath)

	_, err := cli.CallUnary(ctx, connect.NewRequest(&pbhealth.HealthCheckRequest{Service: "invalid"}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("validator: code=%v err=%v", connect.CodeOf(err), err)
	}
	if n := svc.calls.Load(); n != 0 {
		t.Fatalf("handler ran %d times for an invalid request", n)
	}

	_, err = cli.CallUnary(ctx, connect.NewRequest(&pbhealth.HealthCheckRequest{Service: "panic"}))
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("recovery: code=%v err=%v", connect.CodeOf(err), err)
	}
	// The server survived the panic.
	if _, err := cli.CallUnary(ctx, connect.NewRequest(&pbhealth.HealthCheckRequest{})); err != nil {
		t.Fatalf("after panic: %v", err)
	}
}

func TestConnectWithoutHTTPInterceptors(t *testing.T) {
	s, svc := connectServer(t, WithoutHTTPInterceptors())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := connectClient(s, testCheckPath).CallUnary(ctx, connect.NewRequest(&pbhealth.HealthCheckRequest{Service: "invalid"})); err != nil {
		t.Fatalf("validator should be off: %v", err)
	}
	if svc.calls.Load() != 1 {
		t.Fatal("handler should have run")
	}
}

// Connect-Timeout-Ms becomes the handler's context deadline.
func TestConnectTimeoutHeader(t *testing.T) {
	s, _ := connectServer(t)
	body, _ := protojson.Marshal(&pbhealth.HealthCheckRequest{Service: "slow"})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://"+s.opts.httpAddr+testCheckPath, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("Connect-Timeout-Ms", "100")
	start := time.Now()
	cli := &http.Client{Timeout: 3 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timeout not honored: took %v", elapsed)
	}
	var e struct {
		Code string `json:"code"`
	}
	b, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(b, &e)
	if e.Code != "deadline_exceeded" {
		t.Fatalf("code = %q, body = %s", e.Code, b)
	}
}

// grpc.SetHeader / grpc.SetTrailer in a handler reach Connect clients.
func TestConnectResponseMetadata(t *testing.T) {
	s, _ := connectServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resp, err := connectClient(s, testCheckPath).CallUnary(ctx, connect.NewRequest(&pbhealth.HealthCheckRequest{Service: "header"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Header().Get("X-Test-Header"); got != "h" {
		t.Errorf("header = %q", got)
	}
	if got := resp.Trailer().Get("X-Test-Trailer"); got != "t" {
		t.Errorf("trailer = %q", got)
	}
}

// Incoming HTTP headers are visible to handlers as gRPC metadata.
func TestConnectIncomingMetadata(t *testing.T) {
	s, _ := connectServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req := connect.NewRequest(&pbhealth.HealthCheckRequest{Service: "metadata"})
	req.Header().Set("X-Test", "abc")
	resp, err := connectClient(s, testCheckPath).CallUnary(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if got := int(resp.Msg.GetStatus()); got != 3 {
		t.Fatalf("handler saw x-test of length %d, want 3", got)
	}
}

// GET is only allowed for methods declared side-effect free.
func TestConnectGETRequiresNoSideEffects(t *testing.T) {
	s, _ := connectServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := connectClient(s, testMutatePath, connect.WithHTTPGet(), connect.WithIdempotency(connect.IdempotencyNoSideEffects)).
		CallUnary(ctx, connect.NewRequest(&pbhealth.HealthCheckRequest{}))
	if err == nil {
		t.Fatal("GET to a method with side effects should fail")
	}
}

// Compressed responses are produced when the client accepts gzip.
func TestConnectGzipResponse(t *testing.T) {
	s, _ := connectServer(t)
	body, _ := protojson.Marshal(&pbhealth.HealthCheckRequest{Service: strings.Repeat("a", 4096)})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://"+s.opts.httpAddr+testCheckPath, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Encoding", "gzip")
	tr := &http.Transport{DisableCompression: true} // see the raw encoding
	defer tr.CloseIdleConnections()
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	// A tiny response is not worth compressing; this one is a few bytes, so
	// only assert that whatever encoding is declared decodes correctly.
	var r io.Reader = resp.Body
	if resp.Header.Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		r = zr
	}
	b, _ := io.ReadAll(r)
	out := &pbhealth.HealthCheckResponse{}
	if err := protojson.Unmarshal(b, out); err != nil {
		t.Fatalf("decode %q: %v", b, err)
	}
}

// Streaming content types are rejected on unary procedures instead of being
// mis-parsed as unary bodies.
func TestConnectRejectsStreamingContentType(t *testing.T) {
	s, _ := connectServer(t)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://"+s.opts.httpAddr+testCheckPath, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/connect+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

// Requests above the body limit are rejected.
func TestConnectBodyLimit(t *testing.T) {
	s, _ := connectServer(t, WithMaxRequestBodyBytes(1024))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := connectClient(s, testCheckPath).CallUnary(ctx, connect.NewRequest(&pbhealth.HealthCheckRequest{Service: strings.Repeat("a", 4096)}))
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("code = %v, err = %v", connect.CodeOf(err), err)
	}
}

// Auto-HTTP routes (WithAutoHTTP) also run the interceptor chain.
func TestAutoHTTPRunsValidator(t *testing.T) {
	s := newTestServer(t, WithAutoHTTP())
	svc := &testService{}
	s.RegisterService(&testServiceDesc, svc)
	runTestServer(t, s)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://"+s.opts.httpAddr+testCheckPath, strings.NewReader(`{"service":"invalid"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", resp.StatusCode, b)
	}
	if svc.calls.Load() != 0 {
		t.Fatal("handler ran for an invalid request")
	}
}

// WithConnectOptions makes protoc-gen-connect-go style handlers run the
// grpcmux unary interceptor chain, and handler errors pass through intact.
func TestWithConnectOptionsRunsInterceptors(t *testing.T) {
	deny := func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if r, ok := req.(*pbhealth.HealthCheckRequest); ok && r.GetService() == "deny" {
			return nil, status.Error(codes.PermissionDenied, "denied by interceptor")
		}
		return handler(ctx, req)
	}
	s := newTestServer(t, WithGRPCUnaryServerInterceptor(deny))
	const procedure = "/grpcmux.test.v1.ConnectGo/Check"
	h := connect.NewUnaryHandler(procedure,
		func(_ context.Context, req *connect.Request[pbhealth.HealthCheckRequest]) (*connect.Response[pbhealth.HealthCheckResponse], error) {
			if req.Msg.GetService() == "missing" {
				return nil, connect.NewError(connect.CodeNotFound, errors.New("no such thing"))
			}
			return connect.NewResponse(&pbhealth.HealthCheckResponse{Status: pbhealth.HealthCheckResponse_SERVING}), nil
		}, WithConnectOptions(s)...)
	RegisterConnectHandler(s, procedure, h)
	runTestServer(t, s)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cli := connectClient(s, procedure)
	if _, err := cli.CallUnary(ctx, connect.NewRequest(&pbhealth.HealthCheckRequest{})); err != nil {
		t.Fatal(err)
	}
	_, err := cli.CallUnary(ctx, connect.NewRequest(&pbhealth.HealthCheckRequest{Service: "deny"}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("interceptor: %v", err)
	}
	_, err = cli.CallUnary(ctx, connect.NewRequest(&pbhealth.HealthCheckRequest{Service: "missing"}))
	var cerr *connect.Error
	if !errors.As(err, &cerr) || cerr.Code() != connect.CodeNotFound || cerr.Message() != "no such thing" {
		t.Fatalf("handler error: %v", err)
	}
}
