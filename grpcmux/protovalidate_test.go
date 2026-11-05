package grpcmux

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go/buf/validate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	pbhealth "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// A service whose request has protovalidate rules, described at runtime:
//
//	message NamedRequest { string name = 1 [(buf.validate.field).string.min_len = 3]; }
//	service NamedService { rpc Named(NamedRequest) returns (grpc.health.v1.HealthCheckResponse); }
const namedServiceName = "grpcmux.test.v1.NamedService"

var namedRequestDesc protoreflect.MessageDescriptor

func init() {
	fieldOpts := &descriptorpb.FieldOptions{}
	proto.SetExtension(fieldOpts, validate.E_Field, &validate.FieldRules{
		Type: &validate.FieldRules_String_{String_: &validate.StringRules{MinLen: proto.Uint64(3)}},
	})
	fdp := &descriptorpb.FileDescriptorProto{
		Name:    new("grpcmux/test/v1/named.proto"),
		Package: new("grpcmux.test.v1"),
		Syntax:  new("proto3"),
		Dependency: []string{
			validate.File_buf_validate_validate_proto.Path(),
			pbhealth.File_grpc_health_v1_health_proto.Path(),
		},
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: new("NamedRequest"),
			Field: []*descriptorpb.FieldDescriptorProto{{
				Name:     new("name"),
				JsonName: new("name"),
				Number:   proto.Int32(1),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
				Options:  fieldOpts,
			}},
		}},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name: new("NamedService"),
			Method: []*descriptorpb.MethodDescriptorProto{{
				Name:       new("Named"),
				InputType:  new(".grpcmux.test.v1.NamedRequest"),
				OutputType: new(".grpc.health.v1.HealthCheckResponse"),
			}},
		}},
	}
	fd, err := protodesc.NewFile(fdp, protoregistry.GlobalFiles)
	if err != nil {
		panic(err)
	}
	if err := protoregistry.GlobalFiles.RegisterFile(fd); err != nil {
		panic(err)
	}
	namedRequestDesc = fd.Messages().ByName("NamedRequest")
}

type namedServer interface{ named() }

type namedService struct{ calls int }

func (n *namedService) named() {}

var namedServiceDesc = grpc.ServiceDesc{
	ServiceName: namedServiceName,
	HandlerType: (*namedServer)(nil),
	Methods: []grpc.MethodDesc{{
		MethodName: "Named",
		Handler: func(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
			in := dynamicpb.NewMessage(namedRequestDesc)
			if err := dec(in); err != nil {
				return nil, err
			}
			handler := func(context.Context, any) (any, error) {
				srv.(*namedService).calls++
				return &pbhealth.HealthCheckResponse{Status: pbhealth.HealthCheckResponse_SERVING}, nil
			}
			if interceptor == nil {
				return handler(ctx, in)
			}
			return interceptor(ctx, in, &grpc.UnaryServerInfo{Server: srv, FullMethod: "/" + namedServiceName + "/Named"}, handler)
		},
	}},
}

func namedRequest(name string) *dynamicpb.Message {
	m := dynamicpb.NewMessage(namedRequestDesc)
	m.Set(namedRequestDesc.Fields().ByName("name"), protoreflect.ValueOfString(name))
	return m
}

// protovalidate (buf.validate) rules are enforced on native gRPC and on the
// Connect bridge alike.
func TestProtovalidateRules(t *testing.T) {
	s := newTestServer(t)
	svc := &namedService{}
	RegisterConnectService(s, &namedServiceDesc, svc)
	runTestServer(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cc, err := grpc.NewClient(s.opts.grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cc.Close() }()
	path := "/" + namedServiceName + "/Named"
	out := &pbhealth.HealthCheckResponse{}
	if err := cc.Invoke(ctx, path, namedRequest("ab"), out); status.Code(err).String() != "InvalidArgument" {
		t.Fatalf("gRPC, invalid: %v", err)
	}
	if err := cc.Invoke(ctx, path, namedRequest("abc"), out); err != nil {
		t.Fatalf("gRPC, valid: %v", err)
	}

	post := func(body string) (int, string) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+s.opts.httpAddr+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, body := post(`{"name":"ab"}`); code != http.StatusBadRequest || !strings.Contains(body, "invalid_argument") {
		t.Fatalf("Connect, invalid: %d %s", code, body)
	}
	if code, body := post(`{"name":"abc"}`); code != http.StatusOK {
		t.Fatalf("Connect, valid: %d %s", code, body)
	}
	if svc.calls != 2 {
		t.Fatalf("handler calls = %d, want 2 (valid requests only)", svc.calls)
	}
}
