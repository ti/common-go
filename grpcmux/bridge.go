package grpcmux

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// invokeUnary runs one unary method of a registered service for an HTTP
// request, through the generated grpc.MethodDesc handler. That handler
// decodes the request with dec and applies s.httpUnary (the interceptor
// chain) exactly like the gRPC server does, so there is no per-request
// reflection and validation/recovery/metrics behave the same as on gRPC.
//
// The context carries a grpc.ServerTransportStream, so handlers can call
// grpc.Method, grpc.SetHeader and grpc.SetTrailer, and a peer.Peer built from
// the HTTP remote address.
func (s *Server) invokeUnary(ctx context.Context, r *http.Request, srv any, fullMethod string,
	m grpc.MethodDesc, dec func(any) error,
) (proto.Message, *transportStream, error) {
	stream := &transportStream{method: fullMethod}
	ctx = grpc.NewContextWithServerTransportStream(ctx, stream)
	ctx = withHTTPPeer(ctx, r)
	resp, err := m.Handler(srv, ctx, dec, s.httpUnary)
	if err != nil {
		return nil, stream, err
	}
	msg, ok := resp.(proto.Message)
	if !ok {
		return nil, stream, status.Errorf(codes.Internal, "%s returned %T, not a proto.Message", fullMethod, resp)
	}
	return msg, stream, nil
}

// withHTTPPeer adds a peer.Peer for the HTTP client, as gRPC does for its own
// connections (authz policies and handlers read it).
func withHTTPPeer(ctx context.Context, r *http.Request) context.Context {
	if _, ok := peer.FromContext(ctx); ok {
		return ctx
	}
	var addr net.Addr = stringAddr(r.RemoteAddr)
	if ap, err := netip.ParseAddrPort(r.RemoteAddr); err == nil {
		addr = net.TCPAddrFromAddrPort(ap)
	}
	return peer.NewContext(ctx, &peer.Peer{Addr: addr, LocalAddr: localAddr(r)})
}

func localAddr(r *http.Request) net.Addr {
	if a, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		return a
	}
	return nil
}

type stringAddr string

func (a stringAddr) Network() string { return "tcp" }
func (a stringAddr) String() string  { return string(a) }

// transportStream implements grpc.ServerTransportStream for HTTP bridges,
// collecting the header and trailer metadata a handler sets.
type transportStream struct {
	method string

	mu      sync.Mutex
	header  metadata.MD
	trailer metadata.MD
}

func (t *transportStream) Method() string { return t.method }

func (t *transportStream) SetHeader(md metadata.MD) error {
	t.mu.Lock()
	t.header = metadata.Join(t.header, md)
	t.mu.Unlock()
	return nil
}

func (t *transportStream) SendHeader(md metadata.MD) error { return t.SetHeader(md) }

func (t *transportStream) SetTrailer(md metadata.MD) error {
	t.mu.Lock()
	t.trailer = metadata.Join(t.trailer, md)
	t.mu.Unlock()
	return nil
}

func (t *transportStream) metadata() (header, trailer metadata.MD) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.header, t.trailer
}

func invalidArgument(err error) error {
	if _, ok := status.FromError(err); ok && status.Code(err) != codes.Unknown {
		return err
	}
	return status.Error(codes.InvalidArgument, err.Error())
}
