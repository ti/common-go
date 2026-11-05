package grpcmux

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	pbhealth "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

// This file defines a tiny gRPC service used by the grpcmux tests. It is
// hand-written (no protoc) so the tests have no codegen step:
//
//	service grpcmux.test.v1.TestService {
//	  rpc Check(grpc.health.v1.HealthCheckRequest) returns (grpc.health.v1.HealthCheckResponse) {
//	    option idempotency_level = NO_SIDE_EFFECTS;
//	  }
//	  rpc Mutate(grpc.health.v1.HealthCheckRequest) returns (grpc.health.v1.HealthCheckResponse);
//	}
//
// Request "service" values drive behavior:
//
//	"invalid"  → fails Validate() (exercises the validator interceptor)
//	"fail"     → returns NOT_FOUND with an ErrorInfo detail
//	"panic"    → panics (exercises recovery)
//	"slow"     → blocks until the context is done (exercises timeouts)
//	"header"   → sets a response header and trailer via grpc.SetHeader/SetTrailer
//	"metadata" → echoes incoming metadata key "x-test" into the response status string
const (
	testServiceName = "grpcmux.test.v1.TestService"
	testCheckPath   = "/" + testServiceName + "/Check"
	testMutatePath  = "/" + testServiceName + "/Mutate"
)

func init() {
	// Register the descriptor so idempotency lookups (Connect GET) work.
	fdp := &descriptorpb.FileDescriptorProto{
		Name:       new("grpcmux/test/v1/test.proto"),
		Package:    new("grpcmux.test.v1"),
		Syntax:     new("proto3"),
		Dependency: []string{pbhealth.File_grpc_health_v1_health_proto.Path()},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name: new("TestService"),
			Method: []*descriptorpb.MethodDescriptorProto{
				{
					Name:       new("Check"),
					InputType:  new(".grpc.health.v1.HealthCheckRequest"),
					OutputType: new(".grpc.health.v1.HealthCheckResponse"),
					Options: &descriptorpb.MethodOptions{
						IdempotencyLevel: descriptorpb.MethodOptions_NO_SIDE_EFFECTS.Enum(),
					},
				},
				{
					Name:       new("Mutate"),
					InputType:  new(".grpc.health.v1.HealthCheckRequest"),
					OutputType: new(".grpc.health.v1.HealthCheckResponse"),
				},
			},
		}},
	}
	fd, err := protodesc.NewFile(fdp, protoregistry.GlobalFiles)
	if err != nil {
		panic(err)
	}
	if err := protoregistry.GlobalFiles.RegisterFile(fd); err != nil {
		panic(err)
	}
}

// validatedRequest is a HealthCheckRequest with a protoc-gen-validate style
// Validate method, so the validator interceptor has something to check.
type validatedRequest struct {
	*pbhealth.HealthCheckRequest
}

func (r *validatedRequest) Validate() error {
	if r.GetService() == "invalid" {
		return errors.New("service must not be \"invalid\"")
	}
	return nil
}

type testServiceServer interface {
	Check(context.Context, *validatedRequest) (*pbhealth.HealthCheckResponse, error)
	Mutate(context.Context, *validatedRequest) (*pbhealth.HealthCheckResponse, error)
}

type testService struct {
	calls atomic.Int32
}

func (s *testService) Check(ctx context.Context, in *validatedRequest) (*pbhealth.HealthCheckResponse, error) {
	s.calls.Add(1)
	switch in.GetService() {
	case "fail":
		st, _ := status.New(codes.NotFound, "thing not found").WithDetails(&errdetails.ErrorInfo{
			Reason: "THING_MISSING", Domain: "test",
		})
		return nil, st.Err()
	case "panic":
		panic("boom")
	case "slow":
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	case "header":
		_ = grpc.SetHeader(ctx, metadata.Pairs("x-test-header", "h"))
		_ = grpc.SetTrailer(ctx, metadata.Pairs("x-test-trailer", "t"))
	case "metadata":
		md, _ := metadata.FromIncomingContext(ctx)
		if v := md.Get("x-test"); len(v) > 0 {
			return &pbhealth.HealthCheckResponse{Status: pbhealth.HealthCheckResponse_ServingStatus(len(v[0]))}, nil
		}
	}
	return &pbhealth.HealthCheckResponse{Status: pbhealth.HealthCheckResponse_SERVING}, nil
}

func (s *testService) Mutate(ctx context.Context, in *validatedRequest) (*pbhealth.HealthCheckResponse, error) {
	return s.Check(ctx, in)
}

func testUnaryHandler(method string, call func(testServiceServer, context.Context, *validatedRequest) (*pbhealth.HealthCheckResponse, error)) grpc.MethodHandler {
	return func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
		in := &validatedRequest{HealthCheckRequest: &pbhealth.HealthCheckRequest{}}
		if err := dec(in); err != nil {
			return nil, err
		}
		if interceptor == nil {
			return call(srv.(testServiceServer), ctx, in)
		}
		info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/" + testServiceName + "/" + method}
		return interceptor(ctx, in, info, func(ctx context.Context, req any) (any, error) {
			return call(srv.(testServiceServer), ctx, req.(*validatedRequest))
		})
	}
}

var testServiceDesc = grpc.ServiceDesc{
	ServiceName: testServiceName,
	HandlerType: (*testServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{MethodName: "Check", Handler: testUnaryHandler("Check", testServiceServer.Check)},
		{MethodName: "Mutate", Handler: testUnaryHandler("Mutate", testServiceServer.Mutate)},
	},
	Metadata: "grpcmux/test/v1/test.proto",
}

// freeAddr returns a loopback address with a currently-free port.
func freeAddr(t testing.TB) string {
	t.Helper()
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// waitForTCP blocks until addr accepts TCP connections or the timeout elapses.
func waitForTCP(t testing.TB, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c, err := (&net.Dialer{Timeout: 100 * time.Millisecond}).DialContext(context.Background(), "tcp", addr)
		if err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s did not start listening", addr)
}
