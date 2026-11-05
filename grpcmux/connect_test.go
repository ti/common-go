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

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	pbhealth "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoregistry"
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

// testClient makes raw unary calls with a connect-go v2 client (no codegen:
// the Spec is built by hand).
type testClient struct {
	base      string
	path      string
	transport []connecthttp.Option
	idem      connect.IdempotencyLevel
	header    map[string]string
}

type clientOpt func(*testClient)

func withTransport(o ...connecthttp.Option) clientOpt {
	return func(c *testClient) { c.transport = append(c.transport, o...) }
}

// withGET sends side-effect-free calls as HTTP GET.
func withGET() clientOpt {
	return func(c *testClient) {
		c.transport = append(c.transport, connecthttp.WithHTTPGet())
		c.idem = connect.IdempotencyNoSideEffects
	}
}

func withHeader(k, v string) clientOpt {
	return func(c *testClient) {
		if c.header == nil {
			c.header = map[string]string{}
		}
		c.header[k] = v
	}
}

func connectClient(s *Server, path string, opts ...clientOpt) *testClient {
	c := &testClient{base: "http://" + s.opts.httpAddr, path: path}
	for _, o := range opts {
		o(c)
	}
	return c
}

func (c *testClient) call(ctx context.Context, req *pbhealth.HealthCheckRequest) (*pbhealth.HealthCheckResponse, *connect.CallInfo, error) {
	cli := connect.NewClient(connecthttp.NewTransport(http.DefaultClient, c.base, c.transport...))
	ctx, info := connect.NewClientContext(ctx)
	for k, v := range c.header {
		info.RequestHeader().Set(k, v)
	}
	spec := connect.Spec{Procedure: c.path, StreamType: connect.StreamTypeUnary, IdempotencyLevel: c.idem}
	resp := &pbhealth.HealthCheckResponse{}
	err := cli.CallUnary(ctx, spec, req, resp)
	return resp, info, err
}

// A real connect-go client must interoperate in every unary mode it supports.
func TestConnectConformance(t *testing.T) {
	s, _ := connectServer(t)
	cases := []struct {
		name string
		path string
		opts []clientOpt
	}{
		{"proto", testCheckPath, nil},
		{"json", testCheckPath, []clientOpt{withTransport(connecthttp.WithProtoJSON())}},
		{"proto+gzip", testCheckPath, []clientOpt{withTransport(connecthttp.WithSendGzip())}},
		{"json+gzip", testCheckPath, []clientOpt{withTransport(connecthttp.WithProtoJSON(), connecthttp.WithSendGzip())}},
		{"get", testCheckPath, []clientOpt{withGET()}},
		{"get+json", testCheckPath, []clientOpt{withTransport(connecthttp.WithProtoJSON()), withGET()}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			resp, _, err := connectClient(s, tc.path, tc.opts...).call(ctx, &pbhealth.HealthCheckRequest{Service: "x"})
			if err != nil {
				t.Fatal(err)
			}
			if resp.GetStatus() != pbhealth.HealthCheckResponse_SERVING {
				t.Fatalf("status = %v", resp.GetStatus())
			}
		})
	}
}

// Errors keep their code, message and details across the Connect wire format.
func TestConnectErrorDetails(t *testing.T) {
	s, _ := connectServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _, err := connectClient(s, testCheckPath).call(ctx, &pbhealth.HealthCheckRequest{Service: "fail"})
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		t.Fatalf("want *connect.Error, got %v", err)
	}
	if cerr.Code() != connect.CodeNotFound || cerr.Message() != "thing not found" {
		t.Fatalf("code=%v message=%q", cerr.Code(), cerr.Message())
	}
	var found bool
	for _, d := range cerr.Details() {
		v, err := connectproto.UnmarshalErrorDetail(d)
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

	_, _, err := cli.call(ctx, &pbhealth.HealthCheckRequest{Service: "invalid"})
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("validator: code=%v err=%v", connect.CodeOf(err), err)
	}
	if n := svc.calls.Load(); n != 0 {
		t.Fatalf("handler ran %d times for an invalid request", n)
	}

	_, _, err = cli.call(ctx, &pbhealth.HealthCheckRequest{Service: "panic"})
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("recovery: code=%v err=%v", connect.CodeOf(err), err)
	}
	// The server survived the panic.
	if _, _, err := cli.call(ctx, &pbhealth.HealthCheckRequest{}); err != nil {
		t.Fatalf("after panic: %v", err)
	}
}

func TestConnectWithoutHTTPInterceptors(t *testing.T) {
	s, svc := connectServer(t, WithoutHTTPInterceptors())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, _, err := connectClient(s, testCheckPath).call(ctx, &pbhealth.HealthCheckRequest{Service: "invalid"}); err != nil {
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
	_, info, err := connectClient(s, testCheckPath).call(ctx, &pbhealth.HealthCheckRequest{Service: "header"})
	if err != nil {
		t.Fatal(err)
	}
	if got := info.ResponseHeader().Get("X-Test-Header"); got != "h" {
		t.Errorf("header = %q", got)
	}
	if got := info.ResponseTrailer().Get("X-Test-Trailer"); got != "t" {
		t.Errorf("trailer = %q", got)
	}
}

// Incoming HTTP headers are visible to handlers as gRPC metadata.
func TestConnectIncomingMetadata(t *testing.T) {
	s, _ := connectServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resp, _, err := connectClient(s, testCheckPath, withHeader("X-Test", "abc")).
		call(ctx, &pbhealth.HealthCheckRequest{Service: "metadata"})
	if err != nil {
		t.Fatal(err)
	}
	if got := int(resp.GetStatus()); got != 3 {
		t.Fatalf("handler saw x-test of length %d, want 3", got)
	}
}

// GET is only allowed for methods declared side-effect free.
func TestConnectGETRequiresNoSideEffects(t *testing.T) {
	s, _ := connectServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _, err := connectClient(s, testMutatePath, withGET()).
		call(ctx, &pbhealth.HealthCheckRequest{})
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
	_, _, err := connectClient(s, testCheckPath).call(ctx, &pbhealth.HealthCheckRequest{Service: strings.Repeat("a", 4096)})
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

// Handlers registered on ConnectServer (connect-go v2) run the grpcmux unary
// interceptor chain, and handler errors pass through intact.
func TestConnectServerRunsInterceptors(t *testing.T) {
	deny := func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if r, ok := req.(*pbhealth.HealthCheckRequest); ok && r.GetService() == "deny" {
			return nil, status.Error(codes.PermissionDenied, "denied by interceptor")
		}
		return handler(ctx, req)
	}
	s := newTestServer(t, WithGRPCUnaryServerInterceptor(deny))
	md, err := protoregistry.GlobalFiles.FindDescriptorByName(testServiceName + ".Check")
	if err != nil {
		t.Fatal(err)
	}
	procedure := "/" + testServiceName + "/Check"
	var calls int
	s.ConnectServer().Register(connect.Method{
		Spec: connect.Spec{Procedure: procedure, StreamType: connect.StreamTypeUnary, Schema: md},
		Handler: func(_ context.Context, _ connect.Spec, stream connect.ServerStream) error {
			calls++
			req := &pbhealth.HealthCheckRequest{}
			if err := stream.Receive(req); err != nil {
				return err
			}
			if req.GetService() == "missing" {
				return connect.NewError(connect.CodeNotFound, "no such thing")
			}
			return stream.Send(&pbhealth.HealthCheckResponse{Status: pbhealth.HealthCheckResponse_SERVING})
		},
	})
	runTestServer(t, s)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, opts := range [][]clientOpt{nil, {withTransport(connecthttp.WithProtoJSON())}, {withTransport(connecthttp.WithGRPC())}} {
		cli := connectClient(s, procedure, opts...)
		if resp, _, err := cli.call(ctx, &pbhealth.HealthCheckRequest{}); err != nil || resp.GetStatus() != pbhealth.HealthCheckResponse_SERVING {
			t.Fatalf("ok call: %v %v", resp, err)
		}
		_, _, err := cli.call(ctx, &pbhealth.HealthCheckRequest{Service: "deny"})
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("interceptor: %v", err)
		}
		_, _, err = cli.call(ctx, &pbhealth.HealthCheckRequest{Service: "missing"})
		var cerr *connect.Error
		if !errors.As(err, &cerr) || cerr.Code() != connect.CodeNotFound || cerr.Message() != "no such thing" {
			t.Fatalf("handler error: %v", err)
		}
	}
	if calls != 6 {
		t.Fatalf("handler calls = %d, want 6 (denied calls never reach it)", calls)
	}
}
