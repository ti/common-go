package grpcmux

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect/v2/connecthttp"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/ti/common-go/grpcmux/mux"
	"github.com/ti/common-go/log"
	"github.com/ti/common-go/tools/ratelimit"
)

// WithRateLimit limits how often a caller may call the server's methods, on
// every protocol, from declarative rules (the rateLimit key of [Config]):
//
//	grpcmux.WithRateLimit(ratelimit.Config{
//	    Default: &ratelimit.Rule{Rate: "50/s", Burst: 100},
//	    Rules: []ratelimit.Rule{
//	        {Match: "/auth.v1.AuthService/*", Rate: "5/s", Burst: 10, Key: "ip"},
//	    },
//	})
//
// Rules are matched against the RPC's full method ("/pkg.Service/Method";
// for a REST route of [WithREST] that is the method it is bound to) and, for
// other HTTP routes, the URL path. A rule is keyed by the authenticated
// subject (user ID, else client ID of the request's [mux.AuthInfo]; the
// client IP for anonymous requests) and checked after authentication, or
// keyed by client IP and checked before authentication. The client IP is the
// connection's peer unless that is a trusted proxy, in which case it is taken
// from X-Forwarded-For (see ratelimit.Config.TrustedProxies).
//
// A request is counted once: on the gRPC port by interceptors, on the HTTP
// port by the middleware (Connect, gRPC-Web, REST, plain routes), and not
// again when a gateway route calls back through [Server.Conn]. A stream
// counts once, when it is opened.
//
// A limited call fails with RESOURCE_EXHAUSTED, carrying
// google.rpc.RetryInfo and google.rpc.ErrorInfo (reason RATE_LIMIT_EXCEEDED,
// domain "ratelimit") details: HTTP 429 with a Retry-After header for REST
// and Connect unary calls, the retry-after response header plus the status in
// the protocol's own form for gRPC, gRPC-Web and Connect streams.
//
// Buckets live in process memory unless [WithRateLimitStore] supplies a
// shared store. Probes (/healthz, /readyz, grpc.health.v1.Health) are never
// limited. The legacy header-keyed limiter ([WithLimiter] plus
// routerlimit.NewHandler) is independent of this one; use one of the two.
func WithRateLimit(cfg ratelimit.Config) Option {
	return func(o *options) {
		o.platform.rateLimit = &cfg
	}
}

// WithRateLimitStore keeps the rate-limit buckets in a shared store, so the
// limits hold across replicas; (*redis.Redis).RateLimiter() is one. A nil
// store keeps them in memory, so an optional dependency can be passed as is.
// When the store fails, requests are limited in memory until it recovers.
func WithRateLimitStore(store ratelimit.Store) Option {
	return func(o *options) {
		o.platform.rateStore = store
	}
}

// WithRateLimitSubject replaces how the limiter identifies an authenticated
// caller (default: user ID, else client ID, of [mux.AuthInfo]). Return ""
// for a caller without identity; it is then limited by client IP.
func WithRateLimitSubject(fn func(ctx context.Context) string) Option {
	return func(o *options) {
		o.platform.rateSubject = fn
	}
}

// rateLimiter builds the limiter once per option set. A nil limiter means
// rate limiting is off.
func (o *options) rateLimiter() *ratelimit.Limiter {
	p := &o.platform
	if p.limiterBuilt {
		return p.limiter
	}
	p.limiterBuilt = true
	if p.rateLimit == nil {
		return nil
	}
	l, err := ratelimit.New(*p.rateLimit, ratelimit.WithStore(p.rateStore))
	if err != nil {
		// A limiter that silently does not limit is worse than not starting.
		log.Action("grpcmux.Config").Fatal("rateLimit: %v", err)
	}
	p.limiter = l
	return l
}

func (o *options) rateSubject(ctx context.Context) string {
	if o.platform.rateSubject != nil {
		return o.platform.rateSubject(ctx)
	}
	info, ok := mux.AuthInfoFromContext(ctx)
	if !ok {
		return ""
	}
	if id := info.GetUserID(); id != "" {
		return "u:" + id
	}
	if id := info.GetClientID(); id != "" {
		return "c:" + id
	}
	return ""
}

// rateLimitError is the status a limited call fails with.
func rateLimitError(d ratelimit.Decision) *status.Status {
	st := status.New(codes.ResourceExhausted, "rate limit exceeded")
	detailed, err := st.WithDetails(
		&errdetails.RetryInfo{RetryDelay: durationpb.New(d.RetryAfter)},
		&errdetails.ErrorInfo{
			Reason:   ratelimit.Reason,
			Domain:   ratelimit.Domain,
			Metadata: map[string]string{"rule": d.Rule, "key": d.Key},
		},
	)
	if err != nil {
		return st
	}
	return detailed
}

// retryAfterSeconds is the Retry-After value: whole seconds, rounded up, at
// least one (RFC 9110 section 10.2.3 has no finer unit).
func retryAfterSeconds(d time.Duration) string {
	return strconv.FormatInt(max(int64((d+time.Second-1)/time.Second), 1), 10)
}

// memConnNetwork is the network of the in-memory connection's peer address
// (net.Pipe): calls through Server.Conn come from this process, either from
// a gateway route the HTTP middleware has already counted or from the
// service's own code.
const memConnNetwork = "pipe"

// rateLimitInterceptors limit native gRPC calls at one stage.
func rateLimitInterceptors(o *options, l *ratelimit.Limiter, stage ratelimit.Stage,
) (grpc.UnaryServerInterceptor, grpc.StreamServerInterceptor) {
	check := func(ctx context.Context, fullMethod string) error {
		p, ok := peer.FromContext(ctx)
		if !ok || p.Addr == nil || p.Addr.Network() == memConnNetwork {
			return nil
		}
		md, _ := metadata.FromIncomingContext(ctx)
		subject := ""
		if stage == ratelimit.PostAuth {
			subject = o.rateSubject(ctx)
		}
		d := l.Allow(ctx, stage, fullMethod, l.ClientIP(p.Addr.String(), md.Get("x-forwarded-for")), subject)
		if !d.Limited {
			return nil
		}
		_ = grpc.SetHeader(ctx, metadata.Pairs("retry-after", retryAfterSeconds(d.RetryAfter)))
		return rateLimitError(d).Err()
	}
	unary := func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := check(ctx, info.FullMethod); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
	// A stream is charged once, when it is opened.
	stream := func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if err := check(ss.Context(), info.FullMethod); err != nil {
			return err
		}
		return handler(srv, ss)
	}
	return unary, stream
}

// limitHTTP charges an HTTP request at one stage and, when it is over the
// limit, answers it and returns the HTTP status written (0 otherwise).
func (s *Server) limitHTTP(w http.ResponseWriter, r *http.Request, stage ratelimit.Stage) int {
	l := s.opts.rateLimiter()
	if l == nil || r.Method == http.MethodOptions {
		return 0
	}
	subject := ""
	if stage == ratelimit.PostAuth {
		subject = s.opts.rateSubject(r.Context())
	}
	d := l.Allow(r.Context(), stage, rpcMethodOf(r),
		l.ClientIP(r.RemoteAddr, r.Header.Values("X-Forwarded-For")), subject)
	if !d.Limited {
		return 0
	}
	st := rateLimitError(d)
	log.Inject(r.Context(), map[string]any{"rate_limit_rule": d.Rule})
	w.Header().Set("Retry-After", retryAfterSeconds(d.RetryAfter))
	return s.writeRPCError(w, r, st)
}

// rateLimitMiddleware is the post-authentication stage on the HTTP port.
func (s *Server) rateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.limitHTTP(w, r, ratelimit.PostAuth) != 0 {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// rpcErrorWriter writes errors in the gRPC, gRPC-Web and Connect streaming
// wire formats.
var rpcErrorWriter = connecthttp.NewErrorWriter()

// writeRPCError answers a request the server refuses before any handler ran,
// in the form the client's protocol reads, and returns the HTTP status:
//
//   - gRPC, gRPC-Web and Connect streams: HTTP 200 with the status in the
//     trailers or the end-of-stream message, as a handler would answer;
//   - Connect unary procedures: the Connect JSON error with the code's HTTP
//     status;
//   - everything else (REST and plain HTTP routes): the JSON error body of
//     mux.WriteHTTPErrorResponse with the code's HTTP status.
func (s *Server) writeRPCError(w http.ResponseWriter, r *http.Request, st *status.Status) int {
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	switch {
	case r.Method == http.MethodPost &&
		(strings.HasPrefix(ct, "application/grpc") || strings.HasPrefix(ct, "application/connect+")):
		log.Inject(r.Context(), map[string]any{"code": int(st.Code()), "error": st.Message()})
		_ = rpcErrorWriter.Write(w, r, statusToConnect(st.Err()))
		return http.StatusOK
	case s.isConnectProcedure(r):
		log.Inject(r.Context(), map[string]any{"code": int(st.Code()), "error": st.Message()})
		writeConnectError(w, st)
	default:
		gw := s.ServeMux()
		_, outbound := runtime.MarshalerForRequest(gw, r)
		runtime.HTTPError(r.Context(), gw, outbound, w, r, st.Err())
	}
	return mux.CodeToHTTPStatus(st.Code())
}

// isConnectProcedure reports whether the request is for a Connect unary
// procedure: one served by RegisterConnectService or registered on the
// connect-go server (and not reached through a REST route).
func (s *Server) isConnectProcedure(r *http.Request) bool {
	if m := restMatchFrom(r.Context()); m != nil && m.route != nil {
		return false
	}
	s.connectProcsOnce.Do(func() {
		if s.connectProcs == nil {
			s.connectProcs = map[string]bool{}
		}
		if s.connectServer != nil {
			for spec := range s.connectServer.Specs() {
				s.connectProcs[spec.Procedure] = true
			}
		}
	})
	return s.connectProcs[r.URL.Path]
}
