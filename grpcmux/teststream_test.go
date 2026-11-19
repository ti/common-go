package grpcmux

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	pbhealth "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

// A hand-written streaming service for the tests (no protoc):
//
//	service grpcmux.test.v1.StreamService {
//	  rpc Watch(grpc.health.v1.HealthCheckRequest) returns (stream grpc.health.v1.HealthCheckResponse);
//	  rpc Chat(stream grpc.health.v1.HealthCheckRequest) returns (stream grpc.health.v1.HealthCheckResponse);
//	}
//
// Watch sends SERVING at once and then, depending on the request "service":
//
//	""        → blocks until its context ends and returns the context error,
//	            like a watch handler that knows nothing about draining
//	"polite"  → also watches grpcmux.Draining: sends NOT_SERVING and returns nil
//	"header"  → sets response header and trailer metadata, then returns
//	"invalid" → never reached: fails Validate() in the interceptor chain
//
// Chat answers every message with SERVING until the client half-closes.
const (
	streamServiceName = "grpcmux.test.v1.StreamService"
	streamWatchPath   = "/" + streamServiceName + "/Watch"
	streamChatPath    = "/" + streamServiceName + "/Chat"
)

func init() {
	fdp := &descriptorpb.FileDescriptorProto{
		Name:       new("grpcmux/test/v1/stream.proto"),
		Package:    new("grpcmux.test.v1"),
		Syntax:     new("proto3"),
		Dependency: []string{pbhealth.File_grpc_health_v1_health_proto.Path()},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name: new("StreamService"),
			Method: []*descriptorpb.MethodDescriptorProto{
				{
					Name:            new("Watch"),
					InputType:       new(".grpc.health.v1.HealthCheckRequest"),
					OutputType:      new(".grpc.health.v1.HealthCheckResponse"),
					ServerStreaming: new(true),
				},
				{
					Name:            new("Chat"),
					InputType:       new(".grpc.health.v1.HealthCheckRequest"),
					OutputType:      new(".grpc.health.v1.HealthCheckResponse"),
					ClientStreaming: new(true),
					ServerStreaming: new(true),
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

type streamService struct {
	watching atomic.Int32 // Watch handlers currently running
	chatting atomic.Int32
}

var (
	serving    = &pbhealth.HealthCheckResponse{Status: pbhealth.HealthCheckResponse_SERVING}
	notServing = &pbhealth.HealthCheckResponse{Status: pbhealth.HealthCheckResponse_NOT_SERVING}
)

func (s *streamService) watch(req *validatedRequest, stream grpc.ServerStream) error {
	s.watching.Add(1)
	defer s.watching.Add(-1)
	ctx := stream.Context()
	if req.GetService() == "header" {
		_ = stream.SetHeader(metadata.Pairs("x-test-header", "h"))
		stream.SetTrailer(metadata.Pairs("x-test-trailer", "t"))
		return stream.SendMsg(serving)
	}
	if err := stream.SendMsg(serving); err != nil {
		return err
	}
	var draining <-chan struct{}
	if req.GetService() == "polite" {
		draining = Draining(ctx)
	}
	select {
	case <-ctx.Done():
		return status.FromContextError(ctx.Err()).Err()
	case <-draining:
		return stream.SendMsg(notServing)
	}
}

func (s *streamService) chat(stream grpc.ServerStream) error {
	s.chatting.Add(1)
	defer s.chatting.Add(-1)
	for {
		in := &validatedRequest{HealthCheckRequest: &pbhealth.HealthCheckRequest{}}
		if err := stream.RecvMsg(in); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if err := stream.SendMsg(serving); err != nil {
			return err
		}
	}
}

var streamServiceDesc = grpc.ServiceDesc{
	ServiceName: streamServiceName,
	HandlerType: (*any)(nil),
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "Watch",
			ServerStreams: true,
			Handler: func(srv any, stream grpc.ServerStream) error {
				in := &validatedRequest{HealthCheckRequest: &pbhealth.HealthCheckRequest{}}
				if err := stream.RecvMsg(in); err != nil {
					return err
				}
				return srv.(*streamService).watch(in, stream)
			},
		},
		{
			StreamName:    "Chat",
			ServerStreams: true,
			ClientStreams: true,
			Handler: func(srv any, stream grpc.ServerStream) error {
				return srv.(*streamService).chat(stream)
			},
		},
	},
	Metadata: "grpcmux/test/v1/stream.proto",
}

// streamServer boots a server with the stream service on the gRPC port and,
// through RegisterConnectStreams, on the HTTP port.
func streamServer(t *testing.T, opts ...Option) (*Server, *streamService) {
	t.Helper()
	s := newTestServer(t, opts...)
	svc := &streamService{}
	s.RegisterService(&streamServiceDesc, svc)
	RegisterConnectStreams(s, &streamServiceDesc, svc)
	s.RegisterService(&testServiceDesc, &testService{})
	runTestServer(t, s)
	return s, svc
}

// h2cClient speaks HTTP/2 without TLS, which bidirectional streams and the
// gRPC protocol need.
var h2cClient = func() *http.Client {
	p := &http.Protocols{}
	p.SetUnencryptedHTTP2(true)
	return &http.Client{Transport: &http.Transport{Protocols: p}}
}()

// A streamProtocol opens streams on a test server over one wire protocol.
type streamProtocol struct {
	name string
	// open starts a stream on path and sends first (nil: nothing yet).
	open func(t *testing.T, s *Server, ctx context.Context, path string, bidi bool, first *pbhealth.HealthCheckRequest) testStream
}

// testStream is the client side of one stream, whatever the protocol.
type testStream interface {
	// Recv reads the next response; io.EOF is a clean end with status OK.
	Recv() (*pbhealth.HealthCheckResponse, error)
	// Header and Trailer are valid after the stream ended.
	Header(key string) string
	Trailer(key string) string
}

// streamCode names the code a stream ended with, for either error family.
func streamCode(err error) string {
	if err == nil || errors.Is(err, io.EOF) {
		return "OK"
	}
	if cerr, ok := errors.AsType[*connect.Error](err); ok {
		return codes.Code(cerr.Code()).String()
	}
	return status.Code(err).String()
}

type grpcTestStream struct {
	grpc.ClientStream
}

func (g grpcTestStream) Recv() (*pbhealth.HealthCheckResponse, error) {
	out := &pbhealth.HealthCheckResponse{}
	if err := g.RecvMsg(out); err != nil {
		return nil, err
	}
	return out, nil
}

func (g grpcTestStream) Header(key string) string {
	md, _ := g.ClientStream.Header()
	return first(md.Get(key))
}

func (g grpcTestStream) Trailer(key string) string { return first(g.ClientStream.Trailer().Get(key)) }

func first(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

func grpcProtocol(name string, addr func(*Server) string) streamProtocol {
	return streamProtocol{name: name, open: func(t *testing.T, s *Server, ctx context.Context, path string, bidi bool, first *pbhealth.HealthCheckRequest) testStream {
		t.Helper()
		cc, err := grpc.NewClient(addr(s), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cc.Close() })
		st, err := cc.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true, ClientStreams: bidi}, path)
		if err != nil {
			t.Fatalf("%s: open %s: %v", name, path, err)
		}
		if first != nil {
			if err := st.SendMsg(first); err != nil {
				t.Fatalf("%s: send: %v", name, err)
			}
		}
		if !bidi {
			_ = st.CloseSend()
		}
		return grpcTestStream{st}
	}}
}

type connectTestStream struct {
	connect.ClientStream
	info *connect.CallInfo
}

func (c connectTestStream) Recv() (*pbhealth.HealthCheckResponse, error) {
	out := &pbhealth.HealthCheckResponse{}
	if err := c.Receive(out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c connectTestStream) Header(key string) string  { return c.info.ResponseHeader().Get(key) }
func (c connectTestStream) Trailer(key string) string { return c.info.ResponseTrailer().Get(key) }

func connectProtocol(name string, opts ...connecthttp.Option) streamProtocol {
	return connectProtocolOver(name, h2cClient, opts...)
}

func connectProtocolOver(name string, httpClient *http.Client, opts ...connecthttp.Option) streamProtocol {
	return streamProtocol{name: name, open: func(t *testing.T, s *Server, ctx context.Context, path string, bidi bool, first *pbhealth.HealthCheckRequest) testStream {
		t.Helper()
		cli := connect.NewClient(connecthttp.NewTransport(httpClient, "http://"+s.opts.httpAddr, opts...))
		ctx, info := connect.NewClientContext(ctx)
		if tok, ok := ctx.Value(bearerKey{}).(string); ok {
			info.RequestHeader().Set("Authorization", "Bearer "+tok)
		}
		var (
			st  connect.ClientStream
			err error
		)
		if bidi {
			st, err = cli.CallClientStream(ctx, connect.Spec{Procedure: path, StreamType: connect.StreamTypeBidi})
			if err == nil && first != nil {
				err = st.Send(first)
			}
		} else {
			st, err = cli.CallServerStream(ctx, connect.Spec{Procedure: path, StreamType: connect.StreamTypeServer}, first)
		}
		if err != nil {
			t.Fatalf("%s: open %s: %v", name, path, err)
		}
		t.Cleanup(func() { _ = st.Close() })
		return connectTestStream{ClientStream: st, info: info}
	}}
}

// bearerKey carries a token the connect test client sends.
type bearerKey struct{}

// streamProtocols are the ways a client can reach a stream: the gRPC port,
// and gRPC, Connect and gRPC-Web on the HTTP port (HTTP/2 and HTTP/1.1).
var streamProtocols = []streamProtocol{
	grpcProtocol("grpc", func(s *Server) string { return s.opts.grpcAddr }),
	grpcProtocol("grpc-on-http-port", func(s *Server) string { return s.opts.httpAddr }),
	connectProtocol("connect"),
	connectProtocol("connect-json", connecthttp.WithProtoJSON()),
	connectProtocol("grpc-web", connecthttp.WithGRPCWeb()),
	// HTTP/1.1, which is what a browser behind an HTTP/1.1 proxy speaks.
	connectProtocolOver("connect-http1", &http.Client{Transport: &http.Transport{}}),
}

func connecthttpTransport(s *Server, opts ...connecthttp.Option) connect.Transport {
	return connecthttp.NewTransport(h2cClient, "http://"+s.opts.httpAddr, opts...)
}

// streamMessage is the status message of a stream's final error.
func streamMessage(err error) string {
	if cerr, ok := errors.AsType[*connect.Error](err); ok {
		return cerr.Message()
	}
	return status.Convert(err).Message()
}
