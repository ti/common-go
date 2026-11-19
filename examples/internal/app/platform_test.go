package app_test

import (
	"errors"
	"io"
	"slices"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	reflectionv1 "google.golang.org/grpc/reflection/grpc_reflection_v1"

	userv1 "github.com/ti/common-go/examples/gen/go/example/user/v1"
	"github.com/ti/common-go/examples/internal/app"
	"github.com/ti/common-go/grpcmux"
)

// apis.reflection: true serves gRPC reflection on the gRPC port and through
// the HTTP port, which is what grpcurl and buf curl --reflect use.
func TestReflectionFromConfig(t *testing.T) {
	var cfg *app.Config
	startWith(t, func(c *app.Config) {
		c.Apis.Reflection = true
		cfg = c
	})
	for _, addr := range []string{cfg.Apis.GrpcAddr, cfg.Apis.HTTPAddr} {
		cc, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatal(err)
		}
		st, err := reflectionv1.NewServerReflectionClient(cc).ServerReflectionInfo(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Send(&reflectionv1.ServerReflectionRequest{
			MessageRequest: &reflectionv1.ServerReflectionRequest_ListServices{},
		}); err != nil {
			t.Fatal(err)
		}
		resp, err := st.Recv()
		if err != nil {
			t.Fatalf("%s: %v", addr, err)
		}
		var names []string
		for _, s := range resp.GetListServicesResponse().GetService() {
			names = append(names, s.GetName())
		}
		if !slices.Contains(names, userv1.UserService_ServiceDesc.ServiceName) {
			t.Errorf("%s: reflection lists %v, without the UserService", addr, names)
		}
		_ = st.CloseSend()
		_ = cc.Close()
	}
}

// A stream interceptor runs for the streaming method on every protocol: on
// the gRPC port, and for the connect-go generated handler on the HTTP port.
func TestStreamInterceptorRunsOnEveryProtocol(t *testing.T) {
	var streams atomic.Int32
	count := func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if info.FullMethod == userv1.UserService_ExportUsers_FullMethodName {
			streams.Add(1)
		}
		return handler(srv, ss)
	}
	e := startWith(t, nil, grpcmux.WithGRPCStreamServerInterceptors(count))
	ctx := t.Context()
	if _, err := e.grpc.CreateUser(ctx, newUser("a", "a@example.com")); err != nil {
		t.Fatal(err)
	}

	gs, err := e.grpc.ExportUsers(ctx, &userv1.ExportUsersRequest{})
	if err != nil {
		t.Fatal(err)
	}
	drain(t, func() error { _, err := gs.Recv(); return err })
	if streams.Load() != 1 {
		t.Fatalf("after the gRPC stream: interceptor ran %d times, want 1", streams.Load())
	}

	cs, err := e.connect.ExportUsers(ctx, &userv1.ExportUsersRequest{})
	if err != nil {
		t.Fatal(err)
	}
	drain(t, func() error { _, err := cs.Receive(); return err })
	if streams.Load() != 2 {
		t.Fatalf("after the Connect stream: interceptor ran %d times, want 2", streams.Load())
	}
}

// drain reads a stream to its clean end.
func drain(t *testing.T, recv func() error) {
	t.Helper()
	for {
		if err := recv(); errors.Is(err, io.EOF) {
			return
		} else if err != nil {
			t.Fatal(err)
		}
	}
}
