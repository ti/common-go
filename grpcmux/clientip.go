package grpcmux

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"

	"github.com/ti/common-go/log"
	"github.com/ti/common-go/tools/ip"
)

// clientIPMetadata is the incoming-metadata key that carries the client
// address to code that reads metadata (routerlimit rules keyed by "ip").
const clientIPMetadata = "ip"

// trustedProxies parses rateLimit.trustedProxies once per option set. The
// list is honoured whether or not rate limiting itself is on: the same
// address is the access log's "ip" field and what handlers store (ClientIP).
func (o *options) trustedProxies() *ip.TrustedProxies {
	p := &o.platform
	if p.proxiesBuilt {
		return p.proxies
	}
	p.proxiesBuilt = true
	if p.rateLimit == nil {
		return nil
	}
	proxies, err := ip.ParseTrustedProxies(p.rateLimit.TrustedProxies)
	if err != nil {
		// Guessing here would let callers choose the address they are seen as.
		log.Action("grpcmux.Config").Fatal("rateLimit.trustedProxies: %v", err)
	}
	p.proxies = proxies
	return proxies
}

// ClientIP returns the address of the client that made the request being
// handled, on every port and protocol: the connection's peer, or, when the
// peer is one of rateLimit.trustedProxies, the rightmost X-Forwarded-For
// entry that is not a trusted proxy. A caller cannot choose it by sending a
// header (X-Forwarded-For, X-Real-IP, Forwarded, True-Client-IP and the like
// are never taken at their word). Store and log this value; do not read
// those headers in a handler.
//
// It returns "" outside a request and for in-process calls (Server.Conn)
// that did not come from a request.
func ClientIP(ctx context.Context) string {
	clientIP, _ := ip.ClientIPFromContext(ctx)
	return clientIP
}

// clientIPInterceptors work out the client address of a native gRPC call and
// put it into the context (ClientIP) and into the "ip" incoming metadata,
// replacing whatever the caller sent under that key.
func clientIPInterceptors(proxies *ip.TrustedProxies) (grpc.UnaryServerInterceptor, grpc.StreamServerInterceptor) {
	with := func(ctx context.Context) context.Context {
		if _, ok := ip.ClientIPFromContext(ctx); ok {
			return ctx // an HTTP request: the middleware has done it
		}
		md, _ := metadata.FromIncomingContext(ctx)
		p, ok := peer.FromContext(ctx)
		if !ok || p.Addr == nil || p.Addr.Network() == memConnNetwork {
			// Server.Conn: the call comes from this process. A gateway route
			// carries the address the HTTP middleware worked out; keep it.
			if v := md.Get(clientIPMetadata); len(v) > 0 && v[0] != "" {
				return ip.NewContextWithClientIP(ctx, v[0])
			}
			return ctx
		}
		md = md.Copy()
		clientIP := proxies.ClientIP(p.Addr.String(), md.Get("x-forwarded-for"))
		md.Set(clientIPMetadata, clientIP)
		return ip.NewContextWithClientIP(metadata.NewIncomingContext(ctx, md), clientIP)
	}
	unary := func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		return handler(with(ctx), req)
	}
	stream := func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		return handler(srv, &clientIPStream{ServerStream: ss, ctx: with(ss.Context())})
	}
	return unary, stream
}

type clientIPStream struct {
	grpc.ServerStream
	ctx context.Context //nolint:containedctx // a ServerStream carries its context
}

func (s *clientIPStream) Context() context.Context { return s.ctx }
