package grpcmux

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"

	"connectrpc.com/connect/v2"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	reflectionv1 "google.golang.org/grpc/reflection/grpc_reflection_v1"
	reflectionv1alpha "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// RegisterConnectStreams serves the streaming methods of a gRPC service on
// the HTTP port over the Connect, gRPC and gRPC-Web protocols, without
// protoc-gen-connect-go: the generated gRPC stream handlers run on top of the
// connect-go server ([Server.ConnectServer]). Together with
// [RegisterConnectService] one implementation of the generated
// pb.XxxServer interface answers every method on every protocol:
//
//	grpcmux.RegisterConnectService(gs, &pb.NoteService_ServiceDesc, impl) // gRPC + Connect unary
//	grpcmux.RegisterConnectStreams(gs, &pb.NoteService_ServiceDesc, impl) // + streams on the HTTP port
//
// It does not register the service on the gRPC port; RegisterConnectService
// or pb.RegisterXxxServer does. Unary methods are ignored. Handlers see what
// they see on native gRPC: stream.Context() carries incoming metadata, the
// peer and the auth info of the HTTP middleware, and SetHeader, SendHeader
// and SetTrailer write the response metadata. The streams run the same
// interceptor chain as on native gRPC (see WithoutHTTPInterceptors) and are
// drained like every other stream (see WithDrainStreams).
//
// Do not use it for a service whose connect-go generated handler is
// registered on gs.ConnectServer(), or whose stream paths are mounted with
// Handle: the same path cannot be served twice. Call it before Start.
func RegisterConnectStreams(s *Server, desc *grpc.ServiceDesc, serviceImpl any) {
	if len(desc.Streams) > 0 {
		if s.bridgedServices == nil {
			s.bridgedServices = map[string]bool{}
		}
		s.bridgedServices["/"+desc.ServiceName+"/"] = true
	}
	for _, st := range desc.Streams {
		procedure := "/" + desc.ServiceName + "/" + st.StreamName
		s.Logger.Log(s.ctx, logging.LevelDebug, "connect stream", "path", procedure)
		spec := connect.Spec{Procedure: procedure, Schema: methodDescriptor(desc.ServiceName, st.StreamName)}
		if st.ClientStreams {
			spec.StreamType |= connect.StreamTypeClient
		}
		if st.ServerStreams {
			spec.StreamType |= connect.StreamTypeServer
		}
		handler := st.Handler
		s.ConnectServer().Register(connect.Method{
			Spec: spec,
			Handler: func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
				return handler(serviceImpl, newConnectServerStream(ctx, spec, stream))
			},
		})
	}
}

// methodDescriptor returns the protobuf descriptor of a method, or nil when
// its file is not in the global registry.
func methodDescriptor(service, method string) any {
	d, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(service + "." + method))
	if err != nil {
		return nil
	}
	if md, ok := d.(protoreflect.MethodDescriptor); ok {
		return md
	}
	return nil
}

// connectStreamInterceptor runs a gRPC stream interceptor chain around the
// streaming procedures of the connect-go server, so that streams on the HTTP
// port get what streams on the gRPC port get: validation of every received
// message, authz, metrics, custom stream interceptors and panic recovery.
//
// The connect stream is presented to the chain as a grpc.ServerStream
// (connectServerStream); the handler receives a connect stream that goes
// back through whatever the interceptors wrapped around it (grpcBackedStream).
// What connect-go cannot provide is the gRPC transport itself: interceptors
// that reach for transport internals (the HTTP/2 stream, TLS AuthInfo of the
// gRPC port, compression settings via grpc.SetSendCompressor) find none.
func connectStreamInterceptor(chain grpc.StreamServerInterceptor) connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			if spec.StreamType == connect.StreamTypeUnary {
				return next(ctx, spec, stream)
			}
			info := &grpc.StreamServerInfo{
				FullMethod:     spec.Procedure,
				IsClientStream: spec.StreamType&connect.StreamTypeClient != 0,
				IsServerStream: spec.StreamType&connect.StreamTypeServer != 0,
			}
			var handlerErr error
			err := chain(nil, newConnectServerStream(ctx, spec, stream), info,
				func(_ any, ss grpc.ServerStream) error {
					handlerErr = next(ss.Context(), spec, &grpcBackedStream{ServerStream: ss})
					return connectToStatus(handlerErr)
				})
			if err == nil {
				return nil
			}
			// An error the chain passed through unchanged is returned as the
			// handler produced it, with its details; one an interceptor made
			// (validation, recovery, authz) is converted.
			if handlerErr != nil && errors.Is(err, connectToStatus(handlerErr)) {
				return handlerErr
			}
			return statusToConnect(err)
		}
	}
}

// connectServerStream presents a connect-go server stream as a
// grpc.ServerStream.
type connectServerStream struct {
	ctx    context.Context
	stream connect.ServerStream
	md     *connectTransportStream
}

func newConnectServerStream(ctx context.Context, spec connect.Spec, stream connect.ServerStream) *connectServerStream {
	ts := newConnectTransportStream(ctx, spec.Procedure)
	if info, ok := connect.CallInfoForServerContext(ctx); ok {
		ctx = withHTTPPeer(ctx, &http.Request{RemoteAddr: info.PeerAddr})
	}
	return &connectServerStream{
		ctx:    grpc.NewContextWithServerTransportStream(ctx, ts),
		stream: stream,
		md:     ts,
	}
}

func (s *connectServerStream) Context() context.Context { return s.ctx }

func (s *connectServerStream) SetHeader(md metadata.MD) error { return s.md.SetHeader(md) }

func (s *connectServerStream) SendHeader(md metadata.MD) error {
	_ = s.md.SetHeader(md)
	return connectToStatus(s.stream.SendHeaders())
}

func (s *connectServerStream) SetTrailer(md metadata.MD) { _ = s.md.SetTrailer(md) }

func (s *connectServerStream) SendMsg(m any) error {
	return connectToStatus(s.stream.Send(m))
}

func (s *connectServerStream) RecvMsg(m any) error {
	err := s.stream.Receive(m)
	if errors.Is(err, io.EOF) {
		return io.EOF // gRPC handlers compare with ==
	}
	return connectToStatus(err)
}

// grpcBackedStream is the connect stream a handler gets inside the gRPC
// interceptor chain: every operation goes through the grpc.ServerStream the
// interceptors wrapped.
type grpcBackedStream struct {
	grpc.ServerStream
}

func (s *grpcBackedStream) Receive(msg any) error { return s.RecvMsg(msg) }
func (s *grpcBackedStream) Send(msg any) error    { return s.SendMsg(msg) }
func (s *grpcBackedStream) SendHeaders() error    { return s.SendHeader(nil) }

// connectTransportStream implements grpc.ServerTransportStream for calls
// served by connect-go: grpc.SetHeader and grpc.SetTrailer write the call's
// response headers and trailers.
type connectTransportStream struct {
	method string
	info   *connect.CallInfo

	mu sync.Mutex
}

func newConnectTransportStream(ctx context.Context, method string) *connectTransportStream {
	info, _ := connect.CallInfoForServerContext(ctx)
	return &connectTransportStream{method: method, info: info}
}

func (t *connectTransportStream) Method() string { return t.method }

func (t *connectTransportStream) SetHeader(md metadata.MD) error {
	if t.info != nil {
		t.mu.Lock()
		addMetadata(t.info.ResponseHeader(), md)
		t.mu.Unlock()
	}
	return nil
}

func (t *connectTransportStream) SendHeader(md metadata.MD) error { return t.SetHeader(md) }

func (t *connectTransportStream) SetTrailer(md metadata.MD) error {
	if t.info != nil {
		t.mu.Lock()
		addMetadata(t.info.ResponseTrailer(), md)
		t.mu.Unlock()
	}
	return nil
}

// addMetadata copies gRPC metadata into connect headers. Binary (-bin)
// values are base64-encoded as the protocols require.
func addMetadata(h *connect.Header, md metadata.MD) {
	for k, vs := range md {
		if reservedMetadataKey(k) {
			continue
		}
		for _, v := range vs {
			if len(k) > 4 && k[len(k)-4:] == "-bin" {
				v = connect.EncodeBinaryHeader([]byte(v))
			}
			h.Add(k, v)
		}
	}
}

// ReflectionMethods are the auth-exemption patterns of the gRPC reflection
// services, for WithNoAuthMethods(ReflectionMethods...).
var ReflectionMethods = []string{
	"/" + reflectionv1.ServerReflection_ServiceDesc.ServiceName + "/*",
	"/" + reflectionv1alpha.ServerReflection_ServiceDesc.ServiceName + "/*",
}

// registerReflection serves grpc.reflection.v1 and v1alpha (WithReflection)
// on the gRPC port, the in-memory connection and the HTTP port. The service
// list is read from the gRPC server on every call, so services registered
// later are included.
func (s *Server) registerReflection() {
	opts := reflection.ServerOptions{Services: s.grpcServer}
	v1, v1alpha := reflection.NewServerV1(opts), reflection.NewServer(opts)
	s.registerGRPC(&reflectionv1.ServerReflection_ServiceDesc, v1)
	s.registerGRPC(&reflectionv1alpha.ServerReflection_ServiceDesc, v1alpha)
	RegisterConnectStreams(s, &reflectionv1.ServerReflection_ServiceDesc, v1)
	RegisterConnectStreams(s, &reflectionv1alpha.ServerReflection_ServiceDesc, v1alpha)
}
