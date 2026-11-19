package grpcmux

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	pbhealth "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	reflectionv1 "google.golang.org/grpc/reflection/grpc_reflection_v1"
	reflectionv1alpha "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"google.golang.org/grpc/status"
)

// streamsStarted reads grpc_server_started_total for a streaming method.
func streamsStarted(t *testing.T, service, method string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var total float64
	for _, f := range families {
		if f.GetName() != "grpc_server_started_total" {
			continue
		}
		for _, m := range f.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			if labels["grpc_service"] == service && labels["grpc_method"] == method {
				total += m.GetCounter().GetValue()
			}
		}
	}
	return total
}

// Streams on the HTTP port run the interceptor chain of the gRPC port:
// validation of the request, gRPC metrics, custom stream interceptors, and
// they see what a gRPC interceptor sees (full method, stream kind, metadata).
// Before, only unary calls did.
func TestStreamsOnHTTPPortRunInterceptors(t *testing.T) {
	var seen atomic.Int32
	var lastInfo atomic.Pointer[grpc.StreamServerInfo]
	custom := func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		seen.Add(1)
		lastInfo.Store(info)
		if md, _ := metadata.FromIncomingContext(ss.Context()); len(md.Get("x-deny")) > 0 {
			return status.Error(codes.PermissionDenied, "denied by stream interceptor")
		}
		return handler(srv, ss)
	}
	s, svc := streamServer(t, WithGRPCStreamServerInterceptors(custom), WithMetricsAddr(freeAddr(t)))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, p := range streamProtocols[1:] { // everything on the HTTP port
		t.Run(p.name, func(t *testing.T) {
			before, startedBefore := seen.Load(), streamsStarted(t, streamServiceName, "Watch")

			// A valid stream: interceptor ran, metrics counted it, metadata arrives.
			st := p.open(t, s, ctx, streamWatchPath, false, &pbhealth.HealthCheckRequest{Service: "header"})
			if _, err := st.Recv(); err != nil {
				t.Fatalf("first message: %v", err)
			}
			if _, err := st.Recv(); !errors.Is(err, io.EOF) {
				t.Fatalf("end of stream: %v", err)
			}
			if h, tr := st.Header("x-test-header"), st.Trailer("x-test-trailer"); h != "h" || tr != "t" {
				t.Errorf("response metadata: header %q trailer %q, want h and t", h, tr)
			}
			if seen.Load() != before+1 {
				t.Errorf("custom stream interceptor ran %d times, want 1", seen.Load()-before)
			}
			if info := lastInfo.Load(); info.FullMethod != streamWatchPath || !info.IsServerStream || info.IsClientStream {
				t.Errorf("StreamServerInfo = %+v", info)
			}
			if got := streamsStarted(t, streamServiceName, "Watch") - startedBefore; got != 1 {
				t.Errorf("grpc_server_started_total grew by %v, want 1", got)
			}

			// An invalid request is rejected by the validator, before the handler.
			bad := p.open(t, s, ctx, streamWatchPath, false, &pbhealth.HealthCheckRequest{Service: "invalid"})
			if _, err := bad.Recv(); streamCode(err) != "InvalidArgument" {
				t.Errorf("invalid request: %v, want INVALID_ARGUMENT", err)
			}
		})
	}
	if n := svc.watching.Load(); n != 0 {
		t.Errorf("%d handlers still running", n)
	}

	// An interceptor's rejection reaches the client as its status.
	t.Run("interceptor error", func(t *testing.T) {
		cc, err := grpc.NewClient(s.opts.httpAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = cc.Close() }()
		ctx := metadata.AppendToOutgoingContext(ctx, "x-deny", "1")
		st, err := cc.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true}, streamWatchPath)
		if err != nil {
			t.Fatal(err)
		}
		_ = st.SendMsg(&pbhealth.HealthCheckRequest{})
		_ = st.CloseSend()
		err = st.RecvMsg(&pbhealth.HealthCheckResponse{})
		if status.Code(err) != codes.PermissionDenied || !strings.Contains(err.Error(), "denied by stream interceptor") {
			t.Errorf("got %v, want the interceptor's PERMISSION_DENIED", err)
		}
	})
}

// WithoutHTTPInterceptors turns the chain off for streams too.
func TestStreamsOnHTTPPortWithoutInterceptors(t *testing.T) {
	var seen atomic.Int32
	custom := func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		seen.Add(1)
		return handler(srv, ss)
	}
	s, _ := streamServer(t, WithGRPCStreamServerInterceptors(custom), WithoutHTTPInterceptors())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st := streamProtocols[2].open(t, s, ctx, streamWatchPath, false, &pbhealth.HealthCheckRequest{Service: "header"})
	if _, err := st.Recv(); err != nil {
		t.Fatal(err)
	}
	if seen.Load() != 0 {
		t.Errorf("interceptor ran %d times with WithoutHTTPInterceptors", seen.Load())
	}
}

// A bidirectional stream bridged with RegisterConnectStreams works message by
// message on the HTTP port, and every received message is validated.
func TestBidiStreamOnHTTPPort(t *testing.T) {
	s, _ := streamServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cli := connect.NewClient(connecthttpTransport(s))
	st, err := cli.CallClientStream(ctx, connect.Spec{Procedure: streamChatPath, StreamType: connect.StreamTypeBidi})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	for range 3 {
		if err := st.Send(&pbhealth.HealthCheckRequest{}); err != nil {
			t.Fatal(err)
		}
		out := &pbhealth.HealthCheckResponse{}
		if err := st.Receive(out); err != nil || out.GetStatus() != pbhealth.HealthCheckResponse_SERVING {
			t.Fatalf("echo: %v %v", out, err)
		}
	}
	if err := st.Send(&pbhealth.HealthCheckRequest{Service: "invalid"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Receive(&pbhealth.HealthCheckResponse{}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("invalid message mid-stream: %v, want invalid_argument", err)
	}
}

func listServices(ctx context.Context, cc *grpc.ClientConn) ([]string, error) {
	st, err := reflectionv1.NewServerReflectionClient(cc).ServerReflectionInfo(ctx)
	if err != nil {
		return nil, err
	}
	// A Send on a stream the server already ended (an auth rejection)
	// reports io.EOF; the status comes from Recv, so the error of Send is
	// deliberately not returned.
	_ = st.Send(&reflectionv1.ServerReflectionRequest{
		MessageRequest: &reflectionv1.ServerReflectionRequest_ListServices{},
	})
	resp, err := st.Recv()
	if err != nil {
		return nil, err
	}
	_ = st.CloseSend()
	var names []string
	for _, svc := range resp.GetListServicesResponse().GetService() {
		names = append(names, svc.GetName())
	}
	return names, nil
}

func dial(t *testing.T, addr string) *grpc.ClientConn {
	t.Helper()
	cc, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	return cc
}

// Reflection is off by default and, when enabled, answers on the gRPC port
// and through the HTTP port, for v1 and v1alpha.
func TestReflection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	off := newTestServer(t)
	off.RegisterService(&testServiceDesc, &testService{})
	runTestServer(t, off)
	if _, err := listServices(ctx, dial(t, off.opts.grpcAddr)); status.Code(err) != codes.Unimplemented {
		t.Fatalf("reflection without WithReflection: %v, want UNIMPLEMENTED", err)
	}

	on := newTestServer(t, WithReflection())
	on.RegisterService(&testServiceDesc, &testService{}) // registered after NewServer
	runTestServer(t, on)
	for name, addr := range map[string]string{"grpc port": on.opts.grpcAddr, "http port": on.opts.httpAddr} {
		names, err := listServices(ctx, dial(t, addr))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := strings.Join(names, ",")
		for _, want := range []string{testServiceName, "grpc.health.v1.Health", "grpc.reflection.v1.ServerReflection"} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: services %q do not include %s", name, got, want)
			}
		}
		// v1alpha, which older grpcurl and Postman versions use.
		st, err := reflectionv1alpha.NewServerReflectionClient(dial(t, addr)).ServerReflectionInfo(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_ = st.Send(&reflectionv1alpha.ServerReflectionRequest{ //nolint:staticcheck // v1alpha is deprecated but still what older tools speak
			MessageRequest: &reflectionv1alpha.ServerReflectionRequest_FileContainingSymbol{FileContainingSymbol: testServiceName}, //nolint:staticcheck // see above
		})
		resp, err := st.Recv()
		if err != nil || len(resp.GetFileDescriptorResponse().GetFileDescriptorProto()) == 0 { //nolint:staticcheck // see above
			t.Errorf("%s: v1alpha file lookup: %v %v", name, resp, err)
		}
		_ = st.CloseSend()
	}
}

// Reflection publishes the API schema, so it is authenticated like any other
// method unless it is exempted explicitly.
func TestReflectionAndAuth(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s := newTestServer(t, WithReflection(), WithAuthFunc(denyAll))
	runTestServer(t, s)
	for name, addr := range map[string]string{"grpc port": s.opts.grpcAddr, "http port": s.opts.httpAddr} {
		if _, err := listServices(ctx, dial(t, addr)); status.Code(err) != codes.Unauthenticated {
			t.Errorf("%s: reflection without credentials: %v, want UNAUTHENTICATED", name, err)
		}
	}
	open := newTestServer(t, WithReflection(), WithAuthFunc(denyAll), WithNoAuthMethods(ReflectionMethods...))
	runTestServer(t, open)
	for name, addr := range map[string]string{"grpc port": open.opts.grpcAddr, "http port": open.opts.httpAddr} {
		if _, err := listServices(ctx, dial(t, addr)); err != nil {
			t.Errorf("%s: exempted reflection: %v", name, err)
		}
	}
}

// denyAll is an AuthFunc that accepts the bearer token "good" and rejects
// "forbidden" with PERMISSION_DENIED and everything else with UNAUTHENTICATED.
func denyAll(ctx context.Context) (context.Context, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	switch first(md.Get("authorization")) {
	case "Bearer good":
		return ctx, nil
	case "Bearer forbidden":
		return ctx, status.Error(codes.PermissionDenied, "not allowed")
	}
	return ctx, status.Error(codes.Unauthenticated, "missing or invalid token")
}
